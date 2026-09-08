package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/secrets"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
)

const (
	importedSummaryMaxRunes       = 48_000
	importedSummaryMaxResponse    = 256 << 10
	importedSummaryJobTimeout     = 10 * time.Minute
	importedSummaryPromptRevision = "kah-import-summary-v2-batched"
	importedReferenceSectionRunes = 24_000
	importedChunkPageSize         = 500
)

type importedSummaryRun struct {
	Library    model.Library  `json:"library"`
	Provider   model.Provider `json:"provider"`
	SourceHash string         `json:"sourceHash"`
}

func importedRunHash(value any) string {
	data, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

type importedSummaryCheckpoint struct {
	RunHash        string                   `json:"runHash"`
	Version        int                      `json:"version"`
	DocumentID     string                   `json:"documentId"`
	ContentHash    string                   `json:"contentHash"`
	ProviderID     string                   `json:"providerId"`
	PromptRevision string                   `json:"promptRevision"`
	NextBatch      int                      `json:"nextBatch"`
	Candidates     []model.KnowledgePayload `json:"candidates"`
}

func effectiveImportConversionMode(library model.Library) string {
	switch library.ImportConversionMode {
	case "reference", "enhanced", "off":
		if library.ImportConversionMode == "reference" && library.AutoSummarizeImports {
			return "enhanced"
		}
		return library.ImportConversionMode
	default:
		if library.AutoSummarizeImports {
			return "enhanced"
		}
		return "reference"
	}
}

func restoreImportedSummaryCheckpoint(data json.RawMessage, expected importedSummaryCheckpoint, batchCount int) (importedSummaryCheckpoint, int) {
	if len(data) == 0 || batchCount < 1 {
		return expected, 0
	}
	var candidate importedSummaryCheckpoint
	if json.Unmarshal(data, &candidate) != nil || candidate.Version != expected.Version ||
		candidate.DocumentID != expected.DocumentID || candidate.ContentHash != expected.ContentHash ||
		candidate.RunHash != expected.RunHash || candidate.ProviderID != expected.ProviderID || candidate.PromptRevision != expected.PromptRevision ||
		candidate.NextBatch < 0 || candidate.NextBatch > batchCount || len(candidate.Candidates) != candidate.NextBatch {
		return expected, 0
	}
	return candidate, candidate.NextBatch
}

// queueImportedKnowledgeProcessing keeps source ingestion independent from
// model availability. A document is already usable as a source document when
// this returns; the optional KAH stage is durable and can be resumed after a
// restart.
func (s *Server) queueImportedKnowledgeProcessing(ctx context.Context, documentID string) (job model.Job, summaryQueued, referenceCreated bool, err error) {
	detail, err := s.Store.GetDocument(ctx, documentID)
	if err != nil {
		return model.Job{}, false, false, err
	}
	if detail.Status != "ready" || importedReferenceExtract(detail.Preview, 1) == "" {
		return model.Job{}, false, false, nil
	}
	library, err := s.Store.GetLibrary(ctx, detail.LibraryID)
	if err != nil {
		return model.Job{}, false, false, err
	}
	mode := effectiveImportConversionMode(library)
	if mode == "off" {
		return model.Job{}, false, false, nil
	}
	if mode == "enhanced" && strings.TrimSpace(library.SummaryProviderID) != "" {
		// Always create the deterministic reference fallback first. Provider
		// availability or a later batch failure must not make the imported
		// source lose its usable, traceable KAH draft.
		_, referenceCreated, referenceErr := s.createImportedDocumentDraft(ctx, documentID)
		if referenceErr != nil {
			return model.Job{}, false, false, referenceErr
		}
		provider, providerErr := s.Store.GetProvider(ctx, library.SummaryProviderID)
		if providerErr != nil {
			return model.Job{}, false, referenceCreated, providerErr
		}
		provider.APIKey = ""
		chunks, chunksErr := s.loadAllDocumentChunks(ctx, documentID)
		if chunksErr != nil {
			return model.Job{}, false, referenceCreated, chunksErr
		}
		run := importedSummaryRun{Library: library, Provider: provider, SourceHash: importedRunHash(chunks)}
		job, err = s.Store.CreateJob(ctx, "knowledge_summarize", map[string]any{
			"documentId":  documentID,
			"libraryId":   detail.LibraryID,
			"contentHash": detail.ContentHash,
			"run":         run,
		})
		if err != nil {
			return model.Job{}, false, false, err
		}
		go s.runKnowledgeSummarize(job.ID, documentID)
		return job, true, referenceCreated, nil
	}
	_, created, err := s.createImportedDocumentDraft(ctx, documentID)
	return model.Job{}, false, created, err
}

func (s *Server) runKnowledgeSummarize(jobID, documentID string) {
	ctx, cancel := context.WithTimeout(context.Background(), importedSummaryJobTimeout)
	defer cancel()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.05, "Loading imported source")
	if s.stopIfJobCancellationRequested(ctx, jobID) {
		return
	}
	detail, err := s.Store.GetDocument(ctx, documentID)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	detail.Preview, err = s.loadAllDocumentChunks(ctx, detail.ID)
	if err != nil {
		s.failJob(ctx, jobID, fmt.Errorf("load complete imported source: %w", err))
		return
	}
	library, err := s.Store.GetLibrary(ctx, detail.LibraryID)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	var payload struct {
		Run         *importedSummaryRun `json:"run"`
		ContentHash string              `json:"contentHash"`
	}
	if err := s.Store.GetJobPayload(ctx, jobID, &payload); err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	currentRemoteAllowed := library.AllowRemoteModels
	if payload.Run != nil {
		if payload.ContentHash != detail.ContentHash || payload.Run.SourceHash != importedRunHash(detail.Preview) {
			s.failJob(ctx, jobID, errors.New("source extraction changed; create a new conversion run"))
			return
		}
		library = payload.Run.Library
	}
	if effectiveImportConversionMode(library) == "off" {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "KAH conversion is disabled; skipped")
		return
	}
	if effectiveImportConversionMode(library) != "enhanced" || strings.TrimSpace(library.SummaryProviderID) == "" {
		if s.stopIfJobCancellationRequested(ctx, jobID) {
			return
		}
		_, created, fallbackErr := s.createImportedDocumentDraft(ctx, documentID)
		if fallbackErr != nil {
			s.failJob(ctx, jobID, fallbackErr)
			return
		}
		message := "Automatic summary is not configured"
		if created {
			message += "; created a traceable reference draft"
		}
		if !s.stopIfJobCancellationRequested(ctx, jobID) {
			_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, message)
		}
		return
	}
	if s.stopIfJobCancellationRequested(ctx, jobID) {
		return
	}
	provider, err := s.Store.GetProvider(ctx, library.SummaryProviderID)
	if err != nil {
		s.failJob(ctx, jobID, fmt.Errorf("load summary provider: %w", err))
		return
	}
	if payload.Run != nil {
		provider = payload.Run.Provider
	}
	if !provider.Local && (!library.AllowRemoteModels || !currentRemoteAllowed) {
		s.failJob(ctx, jobID, errors.New("remote summarization is not allowed for this library"))
		return
	}
	if payload.Run == nil {
		provider.APIKey = ""
		run := importedSummaryRun{Library: library, Provider: provider, SourceHash: importedRunHash(detail.Preview)}
		if err := s.Store.CaptureJobRun(ctx, jobID, detail.ContentHash, run); err != nil {
			s.failJob(ctx, jobID, err)
			return
		}
	}
	key, err := secrets.Get("provider:" + provider.ID)
	if err != nil {
		s.failJob(ctx, jobID, fmt.Errorf("load summary provider secret: %w", err))
		return
	}
	batches := importedSummaryBatches(detail.Preview, importedSummaryMaxRunes)
	if len(batches) == 0 {
		if !s.stopIfJobCancellationRequested(ctx, jobID) {
			_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "No extractable text was available for an automatic summary")
		}
		return
	}
	language := importedReferenceLanguage(importedReferenceExtract(detail.Preview, importedSummaryMaxRunes))
	runHash := importedRunHash(importedSummaryRun{Library: library, Provider: provider, SourceHash: importedRunHash(detail.Preview)})
	checkpoint := importedSummaryCheckpoint{
		RunHash: runHash,
		Version: 2, DocumentID: detail.ID, ContentHash: detail.ContentHash,
		ProviderID: provider.ID, PromptRevision: importedSummaryPromptRevision,
		Candidates: make([]model.KnowledgePayload, 0, len(batches)),
	}
	startBatch := 0
	if saved, checkpointErr := s.Store.GetJobCheckpoint(ctx, jobID); checkpointErr == nil {
		checkpoint, startBatch = restoreImportedSummaryCheckpoint(saved.Data, checkpoint, len(batches))
	}
	candidates := append([]model.KnowledgePayload(nil), checkpoint.Candidates...)
	if err := s.Store.UpdateJobCheckpoint(ctx, jobID, "running", "enhancement", 0.2+0.45*float64(startBatch)/float64(len(batches)), startBatch, len(batches), "", "", fmt.Sprintf("Preparing summary batches (%d/%d)", startBatch, len(batches)), checkpoint); err != nil {
		s.failJob(ctx, jobID, fmt.Errorf("persist summary checkpoint: %w", err))
		return
	}
	for index := startBatch; index < len(batches); index++ {
		batch := batches[index]
		if s.stopIfJobCancellationRequested(ctx, jobID) {
			return
		}
		material := importedReferenceExtract(batch, importedSummaryMaxRunes)
		progress := 0.2 + 0.45*float64(index)/float64(len(batches))
		_ = s.Store.UpdateJob(ctx, jobID, "running", progress, fmt.Sprintf("Calling configured summary model batch %d/%d", index+1, len(batches)))
		raw, synthErr := s.Providers.SynthesizeKnowledge(ctx, provider, key, detail.Title, storage.DocumentURI(detail.ID), detail.ContentHash, language, material)
		if synthErr != nil {
			s.failJob(ctx, jobID, fmt.Errorf("summary batch %d/%d: %w", index+1, len(batches), synthErr))
			return
		}
		if len([]byte(raw)) > importedSummaryMaxResponse {
			s.failJob(ctx, jobID, fmt.Errorf("summary batch %d/%d response is too large", index+1, len(batches)))
			return
		}
		batchDetail := detail
		batchDetail.Preview = batch
		candidate, normalizeErr := normalizeImportedSummaryCandidate(raw, batchDetail)
		if normalizeErr != nil {
			s.failJob(ctx, jobID, fmt.Errorf("summary batch %d/%d: %w", index+1, len(batches), normalizeErr))
			return
		}
		candidates = append(candidates, candidate)
		checkpoint.NextBatch = index + 1
		checkpoint.Candidates = candidates
		progress = 0.2 + 0.45*float64(checkpoint.NextBatch)/float64(len(batches))
		if checkpointErr := s.Store.UpdateJobCheckpoint(ctx, jobID, "running", "enhancement", progress, checkpoint.NextBatch, len(batches), "", "", fmt.Sprintf("Completed summary model batch %d/%d", checkpoint.NextBatch, len(batches)), checkpoint); checkpointErr != nil {
			s.failJob(ctx, jobID, fmt.Errorf("persist summary batch %d/%d checkpoint: %w", checkpoint.NextBatch, len(batches), checkpointErr))
			return
		}
	}
	if err := s.Store.UpdateJobCheckpoint(ctx, jobID, "running", "merge", 0.7, len(batches), len(batches), "", "", "Merging and validating KAH summary candidates", checkpoint); err != nil {
		s.failJob(ctx, jobID, fmt.Errorf("persist summary merge checkpoint: %w", err))
		return
	}
	candidate, err := mergeImportedSummaryCandidates(candidates, detail)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if s.stopIfJobCancellationRequested(ctx, jobID) {
		return
	}
	submission, duplicate, err := s.Store.CreateKnowledgeDraft(ctx, storage.KnowledgeDraftInput{
		LibraryID:          detail.LibraryID,
		ClientSubmissionID: "document-summary:" + detail.ID + ":" + detail.ContentHash + ":" + provider.ID + ":" + importedSummaryPromptRevision + ":" + runHash,
		Mode:               "create", Payload: candidate, RequireSources: true,
	})
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if s.stopIfJobCancellationRequested(ctx, jobID) {
		return
	}
	if library.AutoReviewAgentSubmissions && strings.TrimSpace(library.ReviewProviderID) != "" {
		reviewID, created, reviewErr := s.Store.EnsureReviewJob(ctx, submission.ID)
		if reviewErr != nil {
			s.failJob(ctx, jobID, fmt.Errorf("queue automatic review: %w", reviewErr))
			return
		}
		if created {
			go s.runKAHKnowledgeReview(reviewID, submission.ID)
		}
	}
	message := "Created KAH summary draft " + submission.ID + "; awaiting review"
	if duplicate {
		message = "Reused existing KAH summary draft " + submission.ID + "; awaiting review"
	}
	if !s.stopIfJobCancellationRequested(ctx, jobID) {
		checkpoint = importedSummaryCheckpoint{
			RunHash: runHash,
			Version: 2, DocumentID: detail.ID, ContentHash: detail.ContentHash,
			ProviderID: provider.ID, PromptRevision: importedSummaryPromptRevision,
			NextBatch: len(batches),
		}
		_ = s.Store.UpdateJobCheckpoint(ctx, jobID, "completed", "completed", 1, len(batches), len(batches), "completed", "", message, checkpoint)
	}
}

func normalizeImportedSummaryCandidate(raw string, detail model.DocumentDetail) (model.KnowledgePayload, error) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "```") {
		lines := strings.Split(value, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
			if last := len(lines) - 1; last >= 0 && strings.TrimSpace(lines[last]) == "```" {
				lines = lines[:last]
			}
			value = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}
	start, end := strings.Index(value, "{"), strings.LastIndex(value, "}")
	if start < 0 || end <= start {
		return model.KnowledgePayload{}, errors.New("summary model did not return a JSON object")
	}
	var candidate model.KnowledgePayload
	if err := json.Unmarshal([]byte(value[start:end+1]), &candidate); err != nil {
		return model.KnowledgePayload{}, fmt.Errorf("decode summary candidate: %w", err)
	}
	if candidate.Title == "" || candidate.Description == "" || candidate.Language == "" {
		return model.KnowledgePayload{}, errors.New("summary candidate must include title, description, and language")
	}
	if len(candidate.Sections) == 0 {
		return model.KnowledgePayload{}, errors.New("summary candidate must include sections")
	}
	for _, section := range candidate.Sections {
		if !strings.Contains(section.Content, "[^source]") {
			return model.KnowledgePayload{}, fmt.Errorf("summary section %q is missing [^source] citation", section.ID)
		}
	}
	language := importedReferenceLanguage(importedReferenceExtract(detail.Preview, importedSummaryMaxRunes))
	candidate.Schema = storage.KAHKnowledgeSchema
	candidate.ID = ""
	candidate.Revision = 0
	candidate.Language = language
	candidate.DuplicateIntent = "independent"
	candidate.Sources = []model.KnowledgeSource{{
		ID: "source", Resource: storage.DocumentURI(detail.ID), Title: detail.Title,
		Locator:  map[string]any{"documentId": detail.ID, "chunkIds": importedChunkIDs(detail.Preview)},
		Snapshot: model.KnowledgeSourceSnapshot{Status: "captured", ContentHash: detail.ContentHash, CapturedAt: time.Now().UTC()},
	}}
	candidate.Verified = nil
	if !containsImportedTag(candidate.Tags, "auto-summary") {
		candidate.Tags = append(candidate.Tags, "auto-summary")
	}
	if !containsImportedTag(candidate.Tags, "imported-source") {
		candidate.Tags = append(candidate.Tags, "imported-source")
	}
	candidate.Generated = model.KnowledgeGenerated{By: "model/import-summary", At: time.Now().UTC()}
	derivation := candidate.Derivation
	if derivation == nil {
		derivation = &model.KnowledgeDerivation{}
	}
	derivation.ID = ""
	derivation.Premises = []string{storage.DocumentURI(detail.ID)}
	derivation.Method = "model-synthesis"
	derivation.Conclusion = candidate.Description
	if strings.TrimSpace(derivation.Limitations) == "" {
		derivation.Limitations = "模型只根据导入资料生成候选知识，发布前必须经过审核。"
	}
	if strings.TrimSpace(derivation.Uncertainty) == "" {
		derivation.Uncertainty = "提炼结果受原始资料完整度和模型理解能力影响。"
	}
	candidate.Derivation = derivation
	return candidate, nil
}

func (s *Server) loadAllDocumentChunks(ctx context.Context, documentID string) ([]model.Chunk, error) {
	chunks := []model.Chunk{}
	for offset := 0; ; offset += importedChunkPageSize {
		page, err := s.Store.ListDocumentChunks(ctx, documentID, offset, importedChunkPageSize)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, page...)
		if len(page) < importedChunkPageSize {
			return importedNonOverlappingChunks(chunks), nil
		}
	}
}

func importedReferenceSections(chunks []model.Chunk, language string) []model.KnowledgeSection {
	intro := "This reference preserves extracted material; it is not an AI summary. Review the cited source before publishing."
	if language == "zh-CN" {
		intro = "此参考条目保留导入资料的提取内容，不代表模型已完成总结；发布前请核对引用的原始资料。"
	}
	sections := []model.KnowledgeSection{}
	part := 0
	var buffer strings.Builder
	flush := func() {
		if strings.TrimSpace(buffer.String()) == "" {
			return
		}
		part++
		id, heading := "overview", "Imported source material"
		if language == "zh-CN" {
			heading = "导入资料"
		}
		if part > 1 {
			id = fmt.Sprintf("source-part-%03d", part)
			heading = fmt.Sprintf("%s（第 %d 部分）", heading, part)
		}
		content := "[^source]\n\n" + strings.TrimSpace(buffer.String())
		if part == 1 {
			content = intro + "[^source]\n\n" + strings.TrimSpace(buffer.String())
		}
		sections = append(sections, model.KnowledgeSection{ID: id, Heading: heading, Content: content})
		buffer.Reset()
	}
	for _, chunk := range chunks {
		text := strings.TrimSpace(importedReferenceChunkText(chunk, language))
		for len([]rune(text)) > importedReferenceSectionRunes {
			partText := string([]rune(text)[:importedReferenceSectionRunes])
			if len([]rune(buffer.String()))+len([]rune(partText)) > importedReferenceSectionRunes {
				flush()
			}
			buffer.WriteString(partText)
			flush()
			text = strings.TrimSpace(string([]rune(text)[importedReferenceSectionRunes:]))
		}
		if text == "" {
			continue
		}
		if len([]rune(buffer.String()))+2+len([]rune(text)) > importedReferenceSectionRunes {
			flush()
		}
		if buffer.Len() > 0 {
			buffer.WriteString("\n\n")
		}
		buffer.WriteString(text)
	}
	flush()
	return sections
}

// importedReferenceChunkText preserves the raw extraction and supplements it
// only with layout facts already carried by the Worker.  In particular, a
// recovered OCR table is rendered as Markdown so reviewers can verify the
// actual row-to-column association without an Agent-generated interpretation.
func importedReferenceChunkText(chunk model.Chunk, language string) string {
	text := strings.TrimSpace(chunk.Text)
	if text == "" {
		return ""
	}
	var builder strings.Builder
	if page, ok := importedReferencePage(chunk.Location["page"]); ok {
		heading := "Page"
		if language == "zh-CN" {
			heading = "第"
			builder.WriteString("#### ")
			builder.WriteString(heading)
			builder.WriteString(strconv.Itoa(page))
			builder.WriteString("页\n\n")
		} else {
			builder.WriteString("#### ")
			builder.WriteString(heading)
			builder.WriteByte(' ')
			builder.WriteString(strconv.Itoa(page))
			builder.WriteString("\n\n")
		}
	}
	builder.WriteString(text)
	for _, table := range importedReferenceTables(chunk.Location) {
		builder.WriteString("\n\n")
		if language == "zh-CN" {
			builder.WriteString("#### 按 OCR 坐标恢复的表格（发布前请核对原件）\n\n")
		} else {
			builder.WriteString("#### Table recovered from OCR coordinates (verify against the source before publishing)\n\n")
		}
		builder.WriteString("| ")
		builder.WriteString(strings.Join(importedReferenceMarkdownCells(table.headers), " | "))
		builder.WriteString(" |\n| ")
		builder.WriteString(strings.TrimRight(strings.Repeat("--- | ", len(table.headers)), " "))
		builder.WriteByte('\n')
		for _, row := range table.rows {
			builder.WriteString("| ")
			builder.WriteString(strings.Join(importedReferenceMarkdownCells(row), " | "))
			builder.WriteString(" |\n")
		}
	}
	return builder.String()
}

type importedReferenceTable struct {
	headers []string
	rows    [][]string
}

func importedReferencePage(value any) (int, bool) {
	switch page := value.(type) {
	case int:
		return page, page > 0
	case int64:
		return int(page), page > 0
	case float64:
		return int(page), page > 0 && page == float64(int(page))
	default:
		return 0, false
	}
}

func importedReferenceTables(location map[string]any) []importedReferenceTable {
	layout, ok := location["ocrLayout"].(map[string]any)
	if !ok {
		return nil
	}
	values, ok := layout["tables"].([]any)
	if !ok {
		return nil
	}
	tables := make([]importedReferenceTable, 0, len(values))
	for _, value := range values {
		table, ok := value.(map[string]any)
		if !ok {
			continue
		}
		headers := importedReferenceStringSlice(table["headers"])
		rowValues, ok := table["rows"].([]any)
		if len(headers) == 0 || !ok {
			continue
		}
		rows := make([][]string, 0, len(rowValues))
		valid := true
		for _, rowValue := range rowValues {
			row := importedReferenceStringSlice(rowValue)
			if len(row) != len(headers) {
				valid = false
				break
			}
			rows = append(rows, row)
		}
		if valid && len(rows) > 0 {
			tables = append(tables, importedReferenceTable{headers: headers, rows: rows})
		}
	}
	return tables
}

func importedReferenceStringSlice(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		items := make([]string, 0, len(values))
		for _, item := range values {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil
			}
			items = append(items, text)
		}
		return items
	default:
		return nil
	}
}

func importedReferenceMarkdownCells(values []string) []string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		cell := strings.TrimSpace(value)
		cell = strings.ReplaceAll(cell, "\\", "\\\\")
		cell = strings.ReplaceAll(cell, "|", "\\|")
		cell = strings.ReplaceAll(cell, "\n", "<br>")
		items = append(items, cell)
	}
	return items
}

func importedSummaryBatches(chunks []model.Chunk, maximum int) [][]model.Chunk {
	if maximum < 1 {
		return nil
	}
	batches := [][]model.Chunk{}
	current := []model.Chunk{}
	used := 0
	flush := func() {
		if len(current) > 0 {
			batches = append(batches, current)
			current = []model.Chunk{}
			used = 0
		}
	}
	for _, chunk := range chunks {
		remaining := []rune(strings.TrimSpace(chunk.Text))
		for len(remaining) > 0 {
			if used == maximum || (len(current) > 0 && maximum-used <= 2) {
				flush()
			}
			if len(current) > 0 {
				used += 2
			}
			take := maximum - used
			if take > len(remaining) {
				take = len(remaining)
			}
			part := chunk
			part.Text = string(remaining[:take])
			current = append(current, part)
			used += take
			remaining = remaining[take:]
			if used == maximum {
				flush()
			}
		}
	}
	flush()
	return batches
}

func mergeImportedSummaryCandidates(candidates []model.KnowledgePayload, detail model.DocumentDetail) (model.KnowledgePayload, error) {
	if len(candidates) == 0 {
		return model.KnowledgePayload{}, errors.New("summary model returned no usable batches")
	}
	merged := candidates[0]
	sections := append([]model.KnowledgeSection(nil), merged.Sections...)
	for batchIndex, candidate := range candidates[1:] {
		for _, section := range candidate.Sections {
			section.ID = fmt.Sprintf("batch-%03d-%s", batchIndex+2, strings.TrimSpace(section.ID))
			sections = append(sections, section)
		}
		for _, tag := range candidate.Tags {
			if !containsImportedTag(merged.Tags, tag) {
				merged.Tags = append(merged.Tags, tag)
			}
		}
	}
	merged.Sections = sections
	merged.Schema = storage.KAHKnowledgeSchema
	merged.ID = ""
	merged.Revision = 0
	merged.Language = importedReferenceLanguage(importedReferenceExtract(detail.Preview, importedSummaryMaxRunes))
	merged.DuplicateIntent = "independent"
	merged.Sources = []model.KnowledgeSource{{
		ID: "source", Resource: storage.DocumentURI(detail.ID), Title: detail.Title,
		Locator: map[string]any{
			"documentId": detail.ID, "chunkIds": importedChunkIDs(detail.Preview), "chunkCount": len(detail.Preview),
		},
		Snapshot: model.KnowledgeSourceSnapshot{Status: "captured", ContentHash: detail.ContentHash, CapturedAt: time.Now().UTC()},
	}}
	// Keep the document-wide source and bind each section to its batch evidence.
	sectionIndex := 0
	for batchIndex, candidate := range candidates {
		sourceID := fmt.Sprintf("source-batch-%03d", batchIndex+1)
		if len(candidate.Sources) > 0 {
			source := candidate.Sources[0]
			source.ID = sourceID
			merged.Sources = append(merged.Sources, source)
			for range candidate.Sections {
				merged.Sections[sectionIndex].Content = strings.ReplaceAll(merged.Sections[sectionIndex].Content, "[^source]", "[^"+sourceID+"]")
				sectionIndex++
			}
		} else {
			sectionIndex += len(candidate.Sections)
		}
	}
	if len(merged.Sources) > 1 {
		merged.Sections[0].Content += "\n\n完整来源：[^source]"
	}
	merged.Verified = nil
	merged.Generated = model.KnowledgeGenerated{By: "model/import-summary", At: time.Now().UTC()}
	method := "model-synthesis"
	if len(candidates) > 1 {
		method = "model-synthesis-batched"
	}
	derivation := merged.Derivation
	if derivation == nil {
		derivation = &model.KnowledgeDerivation{}
	}
	derivation.ID = ""
	derivation.Premises = []string{storage.DocumentURI(detail.ID)}
	derivation.Method = method
	derivation.Conclusion = merged.Description
	if strings.TrimSpace(derivation.Limitations) == "" {
		derivation.Limitations = "模型只根据导入资料生成候选知识，发布前必须经过审核。"
	}
	if strings.TrimSpace(derivation.Uncertainty) == "" {
		derivation.Uncertainty = "提炼结果受原始资料完整度和模型理解能力影响。"
	}
	merged.Derivation = derivation
	return merged, nil
}

func importedChunkIDs(chunks []model.Chunk) []string {
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		ids = append(ids, chunk.ID)
	}
	return ids
}

func containsImportedTag(tags []string, wanted string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), wanted) {
			return true
		}
	}
	return false
}

// Older imports contain retrieval overlap. Remove it only when offsets,
// block location and the exact overlapping text all agree; never guess by text alone.
func importedNonOverlappingChunks(chunks []model.Chunk) []model.Chunk {
	result := make([]model.Chunk, 0, len(chunks))
	var previous model.Chunk
	for _, chunk := range chunks {
		current := chunk
		location := func(value map[string]any) (string, int, int, bool) {
			copy := map[string]any{}
			for key, val := range value {
				if key != "charStart" && key != "charEnd" {
					copy[key] = val
				}
			}
			number := func(key string) (int, bool) {
				switch n := value[key].(type) {
				case float64:
					return int(n), true
				case int:
					return n, true
				}
				return 0, false
			}
			start, okStart := number("charStart")
			end, okEnd := number("charEnd")
			return importedRunHash(copy), start, end, okStart && okEnd
		}
		block, start, _, ok := location(chunk.Location)
		previousBlock, previousStart, end, previousOK := location(previous.Location)
		if ok && previousOK && block == previousBlock && start > previousStart && start < end {
			overlap := end - start
			before, after := []rune(previous.Text), []rune(chunk.Text)
			if overlap <= len(before) && overlap <= len(after) && string(before[len(before)-overlap:]) == string(after[:overlap]) {
				current.Text = string(after[overlap:])
			}
		}
		if strings.TrimSpace(current.Text) != "" {
			result = append(result, current)
		}
		previous = chunk
	}
	return result
}

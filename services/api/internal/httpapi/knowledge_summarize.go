package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	importedSummaryPromptRevision = "kah-import-summary-v1"
)

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
	if library.AutoSummarizeImports && strings.TrimSpace(library.SummaryProviderID) != "" {
		job, err = s.Store.CreateJob(ctx, "knowledge_summarize", map[string]any{
			"documentId":  documentID,
			"libraryId":   detail.LibraryID,
			"contentHash": detail.ContentHash,
		})
		if err != nil {
			return model.Job{}, false, false, err
		}
		go s.runKnowledgeSummarize(job.ID, documentID)
		return job, true, false, nil
	}
	_, created, err := s.createImportedDocumentDraft(ctx, documentID)
	return model.Job{}, false, created, err
}

func (s *Server) runKnowledgeSummarize(jobID, documentID string) {
	ctx, cancel := context.WithTimeout(context.Background(), importedSummaryJobTimeout)
	defer cancel()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.05, "Loading imported source")
	detail, err := s.Store.GetDocument(ctx, documentID)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	library, err := s.Store.GetLibrary(ctx, detail.LibraryID)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if !library.AutoSummarizeImports || strings.TrimSpace(library.SummaryProviderID) == "" {
		_, created, fallbackErr := s.createImportedDocumentDraft(ctx, documentID)
		if fallbackErr != nil {
			s.failJob(ctx, jobID, fallbackErr)
			return
		}
		message := "Automatic summary is not configured"
		if created {
			message += "; created a traceable reference draft"
		}
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, message)
		return
	}
	provider, err := s.Store.GetProvider(ctx, library.SummaryProviderID)
	if err != nil {
		s.failJob(ctx, jobID, fmt.Errorf("load summary provider: %w", err))
		return
	}
	if !provider.Local && !library.AllowRemoteModels {
		s.failJob(ctx, jobID, errors.New("remote summarization is not allowed for this library"))
		return
	}
	key, err := secrets.Get("provider:" + provider.ID)
	if err != nil {
		s.failJob(ctx, jobID, fmt.Errorf("load summary provider secret: %w", err))
		return
	}
	material := importedReferenceExtract(detail.Preview, importedSummaryMaxRunes)
	if material == "" {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "No extractable text was available for an automatic summary")
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.25, "Calling configured summary model")
	raw, err := s.Providers.SynthesizeKnowledge(ctx, provider, key, detail.Title, storage.DocumentURI(detail.ID), detail.ContentHash, importedReferenceLanguage(material), material)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if len([]byte(raw)) > importedSummaryMaxResponse {
		s.failJob(ctx, jobID, errors.New("summary model response is too large"))
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.7, "Validating KAH summary candidate")
	candidate, err := normalizeImportedSummaryCandidate(raw, detail)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	submission, duplicate, err := s.Store.CreateKnowledgeDraft(ctx, storage.KnowledgeDraftInput{
		LibraryID:          detail.LibraryID,
		ClientSubmissionID: "document-summary:" + detail.ID + ":" + detail.ContentHash + ":" + provider.ID + ":" + importedSummaryPromptRevision,
		Mode:               "create", Payload: candidate, RequireSources: true,
	})
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if !duplicate && library.AutoReviewAgentSubmissions && strings.TrimSpace(library.ReviewProviderID) != "" {
		reviewJob, reviewErr := s.Store.CreateJob(ctx, "kah_knowledge_review", map[string]any{"submissionId": submission.ID})
		if reviewErr != nil {
			s.failJob(ctx, jobID, fmt.Errorf("queue automatic review: %w", reviewErr))
			return
		}
		go s.runKAHKnowledgeReview(reviewJob.ID, submission.ID)
	}
	message := "Created KAH summary draft " + submission.ID + "; awaiting review"
	if duplicate {
		message = "Reused existing KAH summary draft " + submission.ID + "; awaiting review"
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, message)
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

func importedChunkIDs(chunks []model.Chunk) []string {
	ids := make([]string, 0, min(8, len(chunks)))
	for _, chunk := range chunks {
		if len(ids) == cap(ids) {
			break
		}
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

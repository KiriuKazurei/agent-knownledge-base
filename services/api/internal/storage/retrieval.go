package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

const retrievalChunkMaxBytes = 12000

func (s *Store) SetRetrievalIndexState(ctx context.Context, libraryID, status, generation, message string) error {
	if strings.TrimSpace(libraryID) == "" || strings.TrimSpace(status) == "" {
		return errors.New("libraryID and retrieval index status are required")
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO retrieval_index_state(library_id,status,error,active_generation,requested_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(library_id) DO UPDATE SET status=excluded.status,error=excluded.error,active_generation=CASE WHEN excluded.active_generation<>'' THEN excluded.active_generation ELSE retrieval_index_state.active_generation END,requested_at=excluded.requested_at,updated_at=excluded.updated_at`, libraryID, status, message, generation, stamp, stamp)
	return err
}

func (s *Store) GetRetrievalIndexState(ctx context.Context, libraryID string) (model.RetrievalGeneration, error) {
	var item model.RetrievalGeneration
	var status, message string
	err := s.DB.QueryRowContext(ctx, `SELECT library_id,status,error,active_generation FROM retrieval_index_state WHERE library_id=?`, libraryID).Scan(&item.LibraryID, &status, &message, &item.Generation)
	item.Status, item.DegradedReason = status, message
	item.Degraded = status == "stale" || status == "failed"
	return item, err
}

// BuildRetrievalCorpus materializes the authoritative, permission-neutral
// corpus for one or more active libraries. Authorization is applied by the
// HTTP/MCP boundary before a query is sent to the Worker; this method still
// excludes deleted and pending records so stale source rows cannot enter a
// generation.
func (s *Store) BuildRetrievalCorpus(ctx context.Context, libraryIDs []string) ([]model.RetrievalChunk, error) {
	libraryIDs = uniqueStrings(libraryIDs)
	if len(libraryIDs) == 0 {
		return nil, errors.New("retrieval library scope is required")
	}
	args := make([]any, 0, len(libraryIDs))
	query := `SELECT c.id,c.document_id,c.ordinal,c.text,c.location_json,c.content_hash,d.library_id,d.title,d.source_path,d.content_hash FROM chunks c JOIN documents d ON d.id=c.document_id JOIN libraries l ON l.id=d.library_id WHERE d.status='ready' AND d.deleted_at IS NULL AND l.deleted_at IS NULL`
	if len(libraryIDs) > 0 {
		query += ` AND d.library_id IN (` + strings.TrimRight(strings.Repeat("?,", len(libraryIDs)), ",") + `)`
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	query += ` ORDER BY d.library_id,d.id,c.ordinal`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chunks := make([]model.RetrievalChunk, 0)
	for rows.Next() {
		var id, documentID, locationJSON, contentHash, libraryID, title, sourcePath, documentHash, text string
		var ordinal int
		if err := rows.Scan(&id, &documentID, &ordinal, &text, &locationJSON, &contentHash, &libraryID, &title, &sourcePath, &documentHash); err != nil {
			return nil, err
		}
		// A citation without a source hash cannot be revalidated after an
		// index generation changes. Exclude malformed legacy chunks and let
		// the caller report a degraded/empty result instead of exposing an
		// unverifiable raw source.
		if strings.TrimSpace(contentHash) == "" {
			continue
		}
		// documents.content_hash is the complete source hash while
		// chunks.content_hash is historically either the chunk hash or the
		// source hash. Do not compare those unlike values; the ready/non-deleted
		// join above is the authoritative liveness check.
		_ = documentHash
		var location map[string]any
		_ = json.Unmarshal([]byte(locationJSON), &location)
		item := model.RetrievalChunk{
			ID: id, DocumentID: documentID, Text: text, SourceType: "raw", ReviewStatus: "unreviewed",
			LibraryID: libraryID, SourceURI: fmt.Sprintf("kah://document/%s#chunk/%d", documentID, ordinal),
			Title: title, FilePath: sourcePath, ContentHash: contentHash, PermissionScope: "library", Location: location,
		}
		item.LineStart, item.LineEnd = locationLines(location)
		item.Language, item.Symbol = locationString(location, "language"), locationString(location, "symbol")
		chunks = append(chunks, splitRetrievalChunk(item)...)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	knowledge, err := s.buildStableKnowledgeChunks(ctx, libraryIDs)
	if err != nil {
		return nil, err
	}
	chunks = append(chunks, knowledge...)
	architecture, err := s.buildArchitectureRetrievalChunks(ctx, libraryIDs)
	if err != nil {
		return nil, err
	}
	return append(chunks, architecture...), nil
}

func (s *Store) buildStableKnowledgeChunks(ctx context.Context, libraryIDs []string) ([]model.RetrievalChunk, error) {
	query := `SELECT ki.library_id,ki.id,kr.revision,kr.payload_json,kr.content_hash FROM knowledge_items ki JOIN libraries l ON l.id=ki.library_id JOIN knowledge_revisions kr ON kr.knowledge_id=ki.id AND kr.revision=ki.stable_revision WHERE kr.status='stable' AND l.deleted_at IS NULL`
	args := make([]any, 0, len(libraryIDs))
	if len(libraryIDs) > 0 {
		query += ` AND ki.library_id IN (` + strings.TrimRight(strings.Repeat("?,", len(libraryIDs)), ",") + `)`
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.RetrievalChunk, 0)
	for rows.Next() {
		var libraryID, knowledgeID, payloadJSON, contentHash string
		var revision int
		if err := rows.Scan(&libraryID, &knowledgeID, &revision, &payloadJSON, &contentHash); err != nil {
			return nil, err
		}
		var payload model.KnowledgePayload
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return nil, fmt.Errorf("decode stable knowledge %s: %w", knowledgeID, err)
		}
		uri := payload.ID
		if uri == "" {
			uri = KnowledgeURI(knowledgeID)
		}
		base := model.RetrievalChunk{LibraryID: libraryID, SourceType: "knowledge", ReviewStatus: "stable", SourceURI: fmt.Sprintf("%s@%d", uri, revision), Title: payload.Title, ContentHash: contentHash, PermissionScope: "library", Language: payload.Language}
		if strings.TrimSpace(payload.Description) != "" {
			base.ID = safeRetrievalID("knowledge-description", uri)
			base.Text = payload.Title + "\n" + payload.Description
			result = append(result, splitRetrievalChunk(base)...)
		}
		for index, section := range payload.Sections {
			if strings.TrimSpace(section.Content) == "" {
				continue
			}
			sectionID := section.ID
			if sectionID == "" {
				sectionID = fmt.Sprintf("section-%d", index)
			}
			item := base
			item.ID = safeRetrievalID("knowledge-section", uri+"|"+sectionID)
			item.SourceURI = fmt.Sprintf("%s@%d#section/%s", uri, revision, sectionID)
			item.Text = strings.TrimSpace(section.Heading + "\n" + section.Content)
			result = append(result, splitRetrievalChunk(item)...)
		}
	}
	return result, rows.Err()
}

func (s *Store) buildArchitectureRetrievalChunks(ctx context.Context, libraryIDs []string) ([]model.RetrievalChunk, error) {
	query := `SELECT p.id,p.library_id,p.name,p.root_path,p.current_analysis_snapshot_id FROM architecture_projects p JOIN libraries l ON l.id=p.library_id WHERE p.deleted_at IS NULL AND l.deleted_at IS NULL AND p.current_analysis_snapshot_id<>''`
	args := make([]any, 0, len(libraryIDs))
	if len(libraryIDs) > 0 {
		query += ` AND p.library_id IN (` + strings.TrimRight(strings.Repeat("?,", len(libraryIDs)), ",") + `)`
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.RetrievalChunk, 0)
	for rows.Next() {
		var projectID, libraryID, name, rootPath, snapshotID string
		if err := rows.Scan(&projectID, &libraryID, &name, &rootPath, &snapshotID); err != nil {
			return nil, err
		}
		items, err := s.architectureProjectChunks(ctx, projectID, libraryID, name, rootPath, snapshotID)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	return result, rows.Err()
}

func (s *Store) architectureProjectChunks(ctx context.Context, projectID, libraryID, name, rootPath, snapshotID string) ([]model.RetrievalChunk, error) {
	snapshot, err := s.GetArchitectureSnapshot(ctx, projectID, snapshotID)
	if err != nil {
		return nil, err
	}
	result := make([]model.RetrievalChunk, 0)
	for _, module := range snapshot.Modules {
		text := fmt.Sprintf("Project %s module %s (%s). Root: %s. Version: %s. Dependencies: %s. Entry points: %s.", name, module.Name, module.Language, module.RootPath, module.Version, strings.Join(module.Dependencies, ", "), strings.Join(module.EntryPoints, ", "))
		result = append(result, splitRetrievalChunk(model.RetrievalChunk{
			ID: safeRetrievalID("architecture-module", projectID+"|"+snapshotID+"|"+module.Name+"|"+module.Language), Text: text, SourceType: "architecture", ReviewStatus: "derived", LibraryID: libraryID, ProjectID: projectID,
			SourceURI: fmt.Sprintf("kah://architecture/%s/%s/module/%s", projectID, snapshotID, module.Name), Title: name + " module " + module.Name, ContentHash: hashText(text), PermissionScope: "project", Language: module.Language,
		})...)
	}
	analysis, analysisErr := s.GetArchitectureAnalysis(ctx, projectID, snapshotID)
	if analysisErr == nil {
		for _, fact := range analysis.Facts {
			text := fact.Subject + " " + fact.Predicate + " " + fact.Object
			result = append(result, splitRetrievalChunk(model.RetrievalChunk{ID: safeRetrievalID("architecture-fact", projectID+"|"+snapshotID+"|"+fact.ID), Text: text, SourceType: "architecture", ReviewStatus: "derived", LibraryID: libraryID, ProjectID: projectID, SourceURI: fmt.Sprintf("kah://architecture/%s/%s/fact/%s", projectID, snapshotID, fact.ID), Title: name + " architecture fact", ContentHash: hashText(text), PermissionScope: "project"})...)
		}
		for _, inference := range analysis.Inferences {
			text := inference.Statement
			result = append(result, splitRetrievalChunk(model.RetrievalChunk{ID: safeRetrievalID("architecture-inference", projectID+"|"+snapshotID+"|"+inference.ID), Text: text, SourceType: "architecture", ReviewStatus: "derived", LibraryID: libraryID, ProjectID: projectID, SourceURI: fmt.Sprintf("kah://architecture/%s/%s/inference/%s", projectID, snapshotID, inference.ID), Title: name + " architecture inference", ContentHash: hashText(text), PermissionScope: "project"})...)
		}
		for _, evidence := range analysis.Evidence {
			item, ok := architectureCodeEvidence(rootPath, snapshot, evidence)
			if ok {
				item.ID = safeRetrievalID("architecture-evidence", projectID+"|"+snapshotID+"|"+evidence.ID)
				item.LibraryID, item.ProjectID, item.Title = libraryID, projectID, name+" code evidence"
				item.SourceURI = fmt.Sprintf("kah://architecture/%s/%s/evidence/%s", projectID, snapshotID, evidence.ID)
				item.ReviewStatus, item.PermissionScope = "derived", "project"
				result = append(result, splitRetrievalChunk(item)...)
			}
		}
	}
	artifacts, err := s.ListArchitectureArtifacts(ctx, projectID, snapshotID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	for _, artifact := range artifacts {
		data, readErr := s.ReadArchitectureArtifact(artifact)
		if readErr != nil {
			continue
		}
		if artifact.Kind != "document" && artifact.Kind != "diagram_json" {
			continue
		}
		if len(data) > 2*1024*1024 || !utf8.Valid(data) {
			continue
		}
		base := model.RetrievalChunk{ID: safeRetrievalID("architecture-artifact", projectID+"|"+snapshotID+"|"+artifact.Kind), Text: string(data), SourceType: "architecture", ReviewStatus: "derived", LibraryID: libraryID, ProjectID: projectID, SourceURI: fmt.Sprintf("kah://architecture/%s/%s/artifact/%s", projectID, snapshotID, artifact.Kind), Title: name + " " + artifact.Kind, FilePath: artifact.RelativePath, ContentHash: artifact.ContentHash, PermissionScope: "project"}
		result = append(result, splitRetrievalChunk(base)...)
	}
	return result, nil
}

func architectureCodeEvidence(root string, snapshot model.ArchitectureSnapshot, evidence model.AnalysisEvidence) (model.RetrievalChunk, bool) {
	var file *model.ArchitectureFile
	for index := range snapshot.Files {
		if snapshot.Files[index].Path == evidence.Path {
			file = &snapshot.Files[index]
			break
		}
	}
	if file == nil || file.ContentHash != evidence.ContentHash || evidence.LineStart < 1 || evidence.LineEnd < evidence.LineStart {
		return model.RetrievalChunk{}, false
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return model.RetrievalChunk{}, false
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return model.RetrievalChunk{}, false
	}
	target, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(evidence.Path)))
	if err != nil || (target != rootAbs && !strings.HasPrefix(target, rootAbs+string(os.PathSeparator))) || hasSymlinkComponent(rootAbs, target) {
		return model.RetrievalChunk{}, false
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil || (resolvedTarget != resolvedRoot && !strings.HasPrefix(resolvedTarget, resolvedRoot+string(os.PathSeparator))) {
		return model.RetrievalChunk{}, false
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return model.RetrievalChunk{}, false
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != evidence.ContentHash {
		return model.RetrievalChunk{}, false
	}
	lines := strings.Split(string(data), "\n")
	if evidence.LineStart > len(lines) || evidence.LineEnd > len(lines) {
		return model.RetrievalChunk{}, false
	}
	return model.RetrievalChunk{Text: strings.Join(lines[evidence.LineStart-1:evidence.LineEnd], "\n"), FilePath: evidence.Path, LineStart: evidence.LineStart, LineEnd: evidence.LineEnd, ContentHash: evidence.ContentHash, Language: file.Language}, true
}

func splitRetrievalChunk(item model.RetrievalChunk) []model.RetrievalChunk {
	text := item.Text
	if len([]byte(text)) <= retrievalChunkMaxBytes {
		if item.ContentHash == "" {
			item.ContentHash = hashText(text)
		}
		return []model.RetrievalChunk{item}
	}
	parts := make([]model.RetrievalChunk, 0, (len(text)/retrievalChunkMaxBytes)+1)
	runes := []rune(text)
	start := 0
	part := 0
	lineStart := item.LineStart
	for start < len(runes) {
		end := start
		bytes := 0
		for end < len(runes) && (end == start || bytes+len([]byte(string(runes[end]))) <= retrievalChunkMaxBytes) {
			bytes += len([]byte(string(runes[end])))
			end++
		}
		value := item
		value.ID = fmt.Sprintf("%s-part-%d", item.ID, part)
		value.Text = string(runes[start:end])
		value.ContentHash = hashText(value.Text)
		value.LineStart = lineStart
		value.LineEnd = lineStart + strings.Count(value.Text, "\n")
		value.TokenCount = len(strings.Fields(value.Text))
		value.TokenLimit = 0
		value.Truncated = false
		parts = append(parts, value)
		lineStart = value.LineEnd + 1
		start, part = end, part+1
	}
	return parts
}

func locationLines(location map[string]any) (int, int) {
	toInt := func(value any) int {
		switch value := value.(type) {
		case float64:
			return int(value)
		case int:
			return value
		case json.Number:
			parsed, _ := value.Int64()
			return int(parsed)
		default:
			return 0
		}
	}
	return toInt(location["lineStart"]), toInt(location["lineEnd"])
}

func locationString(location map[string]any, key string) string {
	value, _ := location[key].(string)
	return value
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func safeRetrievalID(prefix, value string) string {
	return prefix + "-" + hashText(value)[:24]
}

func hasSymlinkComponent(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return true
	}
	current := root
	for _, part := range strings.Split(relative, string(os.PathSeparator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return statErr == nil && info.Mode()&os.ModeSymlink != 0
		}
	}
	return false
}

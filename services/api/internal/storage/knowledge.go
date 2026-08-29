package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const KAHKnowledgeSchema = "kah-knowledge/v1"

var (
	ErrKnowledgeNotFound = errors.New("knowledge not found")
	ErrRevisionNotFound  = errors.New("knowledge revision not found")
)

var requiredKnowledgeSections = map[string][]string{
	"concept":   {"definition"},
	"claim":     {"claim"},
	"procedure": {"goal", "steps"},
	"decision":  {"context", "decision"},
	"policy":    {"rule", "scope"},
	"reference": {"overview"},
}

var knowledgeRelationTypes = map[string]bool{
	"broader": true, "part_of": true, "related": true, "depends_on": true,
	"applies_to": true, "example_of": true, "supports": true, "contradicts": true,
	"derived_from": true, "supersedes": true, "translation_of": true,
}

func KnowledgeURI(id string) string { return "kah://knowledge/" + id }

func ParseKnowledgeURI(value string) (id string, revision int, section string, err error) {
	value = strings.TrimSpace(value)
	fragment := ""
	if before, after, found := strings.Cut(value, "#"); found {
		value, fragment = before, after
	}
	query := ""
	if before, after, found := strings.Cut(value, "?"); found {
		value, query = before, after
	}
	const prefix = "kah://knowledge/"
	if !strings.HasPrefix(value, prefix) {
		return "", 0, "", fmt.Errorf("knowledge URI must start with %s", prefix)
	}
	id = strings.TrimPrefix(value, prefix)
	if _, parseErr := uuid.Parse(id); parseErr != nil {
		return "", 0, "", errors.New("knowledge URI contains an invalid UUID")
	}
	if query != "" {
		parts := strings.Split(query, "&")
		for _, part := range parts {
			key, raw, ok := strings.Cut(part, "=")
			if !ok || key != "revision" {
				return "", 0, "", errors.New("knowledge URI contains an unsupported query parameter")
			}
			parsed, scanErr := strconv.Atoi(raw)
			if scanErr != nil || parsed < 1 || revision != 0 {
				return "", 0, "", errors.New("knowledge revision must be a positive integer")
			}
			revision = parsed
		}
	}
	return id, revision, strings.TrimSpace(fragment), nil
}

func normalizeKnowledgePayload(payload model.KnowledgePayload) (model.KnowledgePayload, []model.KnowledgeValidationIssue) {
	payload.Schema = strings.TrimSpace(payload.Schema)
	payload.ID = strings.TrimSpace(payload.ID)
	payload.Type = strings.TrimSpace(payload.Type)
	payload.Subtype = strings.TrimSpace(payload.Subtype)
	payload.Title = strings.TrimSpace(payload.Title)
	payload.Description = strings.TrimSpace(payload.Description)
	payload.Language = strings.TrimSpace(payload.Language)
	payload.DuplicateIntent = strings.TrimSpace(payload.DuplicateIntent)
	for index := range payload.Aliases {
		payload.Aliases[index] = strings.TrimSpace(payload.Aliases[index])
	}
	for index := range payload.PrimaryPath {
		payload.PrimaryPath[index] = strings.TrimSpace(payload.PrimaryPath[index])
	}
	for index := range payload.Tags {
		payload.Tags[index] = strings.TrimSpace(payload.Tags[index])
	}
	for index := range payload.Sections {
		payload.Sections[index].ID = strings.TrimSpace(strings.ToLower(payload.Sections[index].ID))
		payload.Sections[index].Heading = strings.TrimSpace(payload.Sections[index].Heading)
		payload.Sections[index].Content = strings.TrimSpace(strings.ReplaceAll(payload.Sections[index].Content, "\r\n", "\n"))
	}
	for index := range payload.Sources {
		payload.Sources[index].ID = strings.TrimSpace(payload.Sources[index].ID)
		payload.Sources[index].Resource = strings.TrimSpace(payload.Sources[index].Resource)
		payload.Sources[index].Title = strings.TrimSpace(payload.Sources[index].Title)
	}
	for index := range payload.Relations {
		payload.Relations[index].Type = strings.TrimSpace(payload.Relations[index].Type)
		payload.Relations[index].Target = strings.TrimSpace(payload.Relations[index].Target)
	}
	return payload, []model.KnowledgeValidationIssue{}
}

func ValidateKnowledgePayload(payload model.KnowledgePayload, requireSources bool) model.KnowledgeValidation {
	payload, _ = normalizeKnowledgePayload(payload)
	result := model.KnowledgeValidation{Normalized: payload, Errors: []model.KnowledgeValidationIssue{}, Warnings: []model.KnowledgeValidationIssue{}, NearDuplicates: []model.KnowledgeDirectoryEntry{}}
	add := func(code, path, message string) {
		result.Errors = append(result.Errors, model.KnowledgeValidationIssue{Code: code, Path: path, Message: message})
	}
	if payload.Schema != KAHKnowledgeSchema {
		add("SCHEMA_INVALID", "schema", "schema must be kah-knowledge/v1")
	}
	if _, ok := requiredKnowledgeSections[payload.Type]; !ok {
		add("SCHEMA_INVALID", "type", "type must be concept, claim, procedure, decision, policy, or reference")
	}
	if payload.Subtype != "" && !strings.Contains(payload.Subtype, ":") {
		add("SCHEMA_INVALID", "subtype", "subtype must use a namespace such as software:compatibility")
	}
	if payload.Title == "" || len([]rune(payload.Title)) > 200 {
		add("SCHEMA_INVALID", "title", "title is required and must be at most 200 characters")
	}
	if payload.Description == "" || len([]rune(payload.Description)) > 1000 {
		add("SCHEMA_INVALID", "description", "description is required and must be at most 1000 characters")
	}
	if payload.Language != "zh-CN" && payload.Language != "en" {
		add("SCHEMA_INVALID", "language", "language must be zh-CN or en")
	}
	if len(payload.Sections) == 0 {
		add("SCHEMA_INVALID", "sections", "at least one section is required")
	}
	sectionIDs := map[string]bool{}
	for _, section := range payload.Sections {
		if section.ID == "" || section.Heading == "" || section.Content == "" {
			add("SCHEMA_INVALID", "sections", "each section needs id, heading, and content")
			continue
		}
		if sectionIDs[section.ID] {
			add("SCHEMA_INVALID", "sections", "section IDs must be unique")
		}
		sectionIDs[section.ID] = true
	}
	for _, required := range requiredKnowledgeSections[payload.Type] {
		if !sectionIDs[required] {
			add("SCHEMA_INVALID", "sections", "type "+payload.Type+" requires section "+required)
		}
	}
	sourceIDs := map[string]bool{}
	for _, source := range payload.Sources {
		if source.ID == "" || source.Resource == "" {
			add("SCHEMA_INVALID", "sources", "each source needs id and resource")
			continue
		}
		if sourceIDs[source.ID] {
			add("SCHEMA_INVALID", "sources", "source IDs must be unique")
		}
		sourceIDs[source.ID] = true
		if !strings.HasPrefix(source.Resource, "https://") && !strings.HasPrefix(source.Resource, "kah://knowledge/") {
			add("SCHEMA_INVALID", "sources", "sources must be https URLs or KAH knowledge URIs")
		}
		if strings.HasPrefix(source.Resource, "https://") {
			parsed, parseErr := url.Parse(source.Resource)
			if parseErr != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
				add("SCHEMA_INVALID", "sources", "HTTPS sources must be valid URLs without user information")
			}
		}
		if strings.HasPrefix(source.Resource, "kah://knowledge/") {
			_, exactRevision, _, uriErr := ParseKnowledgeURI(source.Resource)
			if uriErr != nil || exactRevision == 0 {
				add("SCHEMA_INVALID", "sources", "KAH sources must pin an exact revision")
			}
		}
	}
	if requireSources && len(payload.Sources) == 0 {
		add("SCHEMA_INVALID", "sources", "Agent submissions require at least one source")
	}
	body := make([]string, 0, len(payload.Sections))
	for _, section := range payload.Sections {
		body = append(body, section.Content)
	}
	joinedBody := strings.Join(body, "\n")
	for sourceID := range sourceIDs {
		if !strings.Contains(joinedBody, "[^"+sourceID+"]") {
			add("SCHEMA_INVALID", "sections", "source "+sourceID+" must be cited in the body as [^"+sourceID+"]")
		}
	}
	for _, relation := range payload.Relations {
		if !knowledgeRelationTypes[relation.Type] {
			add("SCHEMA_INVALID", "relations", "unsupported relation type "+relation.Type)
		}
		_, targetURIRevision, _, uriErr := ParseKnowledgeURI(relation.Target)
		if uriErr != nil {
			add("SCHEMA_INVALID", "relations", "relation targets must be KAH knowledge URIs")
		}
		if (relation.Type == "supports" || relation.Type == "contradicts" || relation.Type == "derived_from" || relation.Type == "supersedes") && relation.TargetRevision < 1 {
			add("SCHEMA_INVALID", "relations", "evidence relations must pin target_revision")
		}
		if relation.TargetRevision > 0 && targetURIRevision > 0 && relation.TargetRevision != targetURIRevision {
			add("SCHEMA_INVALID", "relations", "target_revision must agree with the URI revision")
		}
	}
	result.Valid = len(result.Errors) == 0
	return result
}

// knowledgeContentHash identifies semantic content only. IDs, revision
// numbers, generated timestamps, and source snapshots are workflow metadata;
// including them would make retries and equivalent submissions look unique.
// Verification records and duplicate intent are also workflow metadata rather
// than knowledge meaning, so they are excluded from the identity.
func knowledgeContentHash(payload model.KnowledgePayload) (string, error) {
	semantic := payload
	semantic.ID = ""
	semantic.Revision = 0
	semantic.Generated = model.KnowledgeGenerated{}
	semantic.Verified = nil
	semantic.DuplicateIntent = ""
	semantic.Sources = append([]model.KnowledgeSource(nil), payload.Sources...)
	for index := range semantic.Sources {
		semantic.Sources[index].Snapshot = model.KnowledgeSourceSnapshot{}
	}
	if semantic.Derivation != nil {
		derivation := *semantic.Derivation
		derivation.ID = ""
		semantic.Derivation = &derivation
	}
	bytes, err := json.Marshal(semantic)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:]), nil
}

type KnowledgeDraftInput struct {
	LibraryID          string
	TokenID            string
	ClientSubmissionID string
	Mode               string
	BaseURI            string
	Payload            model.KnowledgePayload
	RequireSources     bool
}

func (s *Store) CreateKnowledgeDraft(ctx context.Context, input KnowledgeDraftInput) (model.KAHSubmission, bool, error) {
	s.kahDraftMu.Lock()
	defer s.kahDraftMu.Unlock()
	if input.Mode != "create" && input.Mode != "propose_revision" {
		return model.KAHSubmission{}, false, errors.New("mode must be create or propose_revision")
	}
	if strings.TrimSpace(input.ClientSubmissionID) == "" {
		return model.KAHSubmission{}, false, errors.New("idempotency key is required")
	}
	validation := ValidateKnowledgePayload(input.Payload, input.RequireSources)
	if !validation.Valid {
		return model.KAHSubmission{Validation: validation}, false, errors.New("knowledge payload is invalid")
	}
	payload := validation.Normalized
	if input.TokenID != "" {
		var existingID string
		err := s.DB.QueryRowContext(ctx, `SELECT id FROM kah_submissions WHERE submitted_by_token_id=? AND client_submission_id=?`, input.TokenID, input.ClientSubmissionID).Scan(&existingID)
		if err == nil {
			item, getErr := s.GetKAHSubmission(ctx, existingID)
			return item, true, getErr
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return model.KAHSubmission{}, false, err
		}
	}
	knowledgeID := ""
	revision := 1
	if input.Mode == "create" {
		if payload.ID == "" {
			knowledgeID = uuid.NewString()
			payload.ID = KnowledgeURI(knowledgeID)
		} else {
			var parseErr error
			var requestedRevision int
			var section string
			knowledgeID, requestedRevision, section, parseErr = ParseKnowledgeURI(payload.ID)
			if parseErr != nil {
				return model.KAHSubmission{}, false, parseErr
			}
			if requestedRevision != 0 || section != "" {
				return model.KAHSubmission{}, false, errors.New("create knowledge ID must not include a revision or section")
			}
			payload.ID = KnowledgeURI(knowledgeID)
		}
		var exists int
		err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM knowledge_items WHERE id=?`, knowledgeID).Scan(&exists)
		if err == nil {
			return model.KAHSubmission{}, false, errors.New("knowledge URI already exists; use propose_revision")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return model.KAHSubmission{}, false, err
		}
	} else {
		var err error
		var baseRevision int
		var baseSection string
		knowledgeID, baseRevision, baseSection, err = ParseKnowledgeURI(input.BaseURI)
		if err != nil {
			return model.KAHSubmission{}, false, err
		}
		if baseSection != "" {
			return model.KAHSubmission{}, false, errors.New("base URI must not include a section")
		}
		var libraryID string
		if err := s.DB.QueryRowContext(ctx, `SELECT library_id FROM knowledge_items WHERE id=?`, knowledgeID).Scan(&libraryID); err != nil {
			return model.KAHSubmission{}, false, ErrKnowledgeNotFound
		}
		if libraryID != input.LibraryID {
			return model.KAHSubmission{}, false, errors.New("knowledge belongs to another library")
		}
		if baseRevision > 0 {
			var baseStatus string
			if err := s.DB.QueryRowContext(ctx, `SELECT status FROM knowledge_revisions WHERE knowledge_id=? AND revision=?`, knowledgeID, baseRevision).Scan(&baseStatus); err != nil {
				return model.KAHSubmission{}, false, ErrRevisionNotFound
			}
			if baseStatus != "stable" && baseStatus != "deprecated" {
				return model.KAHSubmission{}, false, errors.New("base revision is not published")
			}
		}
		if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM knowledge_revisions WHERE knowledge_id=?`, knowledgeID).Scan(&revision); err != nil {
			return model.KAHSubmission{}, false, err
		}
		payload.ID = KnowledgeURI(knowledgeID)
	}
	payload.Revision = revision
	if payload.Generated.At.IsZero() {
		payload.Generated.At = time.Now().UTC()
	}
	if payload.Generated.By == "" && input.TokenID != "" {
		payload.Generated.By = "agent/" + input.TokenID
	}
	if payload.Derivation != nil && payload.Derivation.ID == "" {
		derivation := *payload.Derivation
		derivation.ID = uuid.NewString()
		payload.Derivation = &derivation
	}
	validation.Normalized = payload
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return model.KAHSubmission{}, false, err
	}
	contentHash, err := knowledgeContentHash(payload)
	if err != nil {
		return model.KAHSubmission{}, false, err
	}
	var existingKnowledgeID string
	err = s.DB.QueryRowContext(ctx, `SELECT knowledge_id FROM knowledge_content_dedup WHERE library_id=? AND content_hash=?`, input.LibraryID, contentHash).Scan(&existingKnowledgeID)
	if err == nil {
		validation.ExactDuplicate = KnowledgeURI(existingKnowledgeID)
		return model.KAHSubmission{Validation: validation}, false, errors.New("EXACT_DUPLICATE")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.KAHSubmission{}, false, err
	}
	near, err := s.findNearKnowledge(ctx, input.LibraryID, payload.Title, knowledgeID)
	if err != nil {
		return model.KAHSubmission{}, false, err
	}
	validation.NearDuplicates = near
	if len(near) > 0 && input.Mode == "create" && payload.DuplicateIntent == "" {
		validation.Errors = append(validation.Errors, model.KnowledgeValidationIssue{Code: "NEAR_DUPLICATE_REQUIRES_INTENT", Path: "duplicate_intent", Message: "choose revision, supplement, or independent for a near duplicate"})
		validation.Valid = false
		return model.KAHSubmission{Validation: validation}, false, errors.New("NEAR_DUPLICATE_REQUIRES_INTENT")
	}
	flags := knowledgeFlags(payload)
	markdown, err := renderKnowledgeMarkdown(payload)
	if err != nil {
		return model.KAHSubmission{}, false, err
	}
	t, stamp := now()
	submission := model.KAHSubmission{ID: uuid.NewString(), LibraryID: input.LibraryID, KnowledgeURI: payload.ID, Revision: revision, Mode: input.Mode, ReviewStatus: "pending_review", Validation: validation, SubmittedByTokenID: input.TokenID, ClientSubmissionID: input.ClientSubmissionID, CreatedAt: t, UpdatedAt: t}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.KAHSubmission{}, false, err
	}
	defer tx.Rollback()
	if input.Mode == "create" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_items(id,library_id,stable_revision,created_at,updated_at) VALUES(?,?,NULL,?,?)`, knowledgeID, input.LibraryID, stamp, stamp); err != nil {
			return model.KAHSubmission{}, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_revisions(knowledge_id,revision,payload_json,markdown,content_hash,status,flags_json,submitted_by,created_at) VALUES(?,?,?,?,?,'draft',?,?,?)`, knowledgeID, revision, string(payloadBytes), markdown, contentHash, mustJSON(flags), input.TokenID, stamp); err != nil {
		return model.KAHSubmission{}, false, err
	}
	for ordinal, section := range payload.Sections {
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_sections(knowledge_id,revision,section_id,heading,content,ordinal) VALUES(?,?,?,?,?,?)`, knowledgeID, revision, section.ID, section.Heading, section.Content, ordinal); err != nil {
			return model.KAHSubmission{}, false, err
		}
	}
	for _, source := range payload.Sources {
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_sources(knowledge_id,revision,source_id,resource,title,locator_json,snapshot_json) VALUES(?,?,?,?,?,?,?)`, knowledgeID, revision, source.ID, source.Resource, source.Title, mustJSON(source.Locator), mustJSON(source.Snapshot)); err != nil {
			return model.KAHSubmission{}, false, err
		}
	}
	for _, relation := range payload.Relations {
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_relations(knowledge_id,revision,relation_type,target_uri,target_revision) VALUES(?,?,?,?,?)`, knowledgeID, revision, relation.Type, relation.Target, relation.TargetRevision); err != nil {
			return model.KAHSubmission{}, false, err
		}
	}
	dedupInsert, err := tx.ExecContext(ctx, `INSERT INTO knowledge_content_dedup(library_id,content_hash,knowledge_id,revision) VALUES(?,?,?,?) ON CONFLICT(library_id,content_hash) DO NOTHING`, input.LibraryID, contentHash, knowledgeID, revision)
	if err != nil {
		return model.KAHSubmission{}, false, err
	}
	inserted, err := dedupInsert.RowsAffected()
	if err != nil {
		return model.KAHSubmission{}, false, err
	}
	if inserted == 0 {
		var duplicateID string
		if duplicateErr := tx.QueryRowContext(ctx, `SELECT knowledge_id FROM knowledge_content_dedup WHERE library_id=? AND content_hash=?`, input.LibraryID, contentHash).Scan(&duplicateID); duplicateErr == nil {
			validation.ExactDuplicate = KnowledgeURI(duplicateID)
			return model.KAHSubmission{Validation: validation}, false, errors.New("EXACT_DUPLICATE")
		}
		return model.KAHSubmission{}, false, errors.New("knowledge content deduplication failed")
	}
	if payload.Derivation != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_derivations(id,knowledge_id,revision,derivation_json) VALUES(?,?,?,?)`, payload.Derivation.ID, knowledgeID, revision, mustJSON(payload.Derivation)); err != nil {
			return model.KAHSubmission{}, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO kah_submissions(id,knowledge_id,revision,library_id,submitted_by_token_id,client_submission_id,mode,review_status,validation_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'pending_review',?,?,?)`, submission.ID, knowledgeID, revision, input.LibraryID, input.TokenID, input.ClientSubmissionID, input.Mode, mustJSON(validation), stamp, stamp); err != nil {
		return model.KAHSubmission{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return model.KAHSubmission{}, false, err
	}
	return submission, false, nil
}

func knowledgeFlags(payload model.KnowledgePayload) []string {
	flags := []string{}
	for _, source := range payload.Sources {
		if strings.HasPrefix(source.Resource, "https://") && source.Snapshot.Status != "verified" {
			flags = append(flags, "source-unverified")
			break
		}
	}
	sort.Strings(flags)
	return flags
}

func renderKnowledgeMarkdown(payload model.KnowledgePayload) (string, error) {
	frontmatter := map[string]any{"schema": payload.Schema, "id": payload.ID, "revision": payload.Revision, "type": payload.Type, "title": payload.Title, "description": payload.Description, "language": payload.Language}
	if payload.Subtype != "" {
		frontmatter["subtype"] = payload.Subtype
	}
	if len(payload.Aliases) > 0 {
		frontmatter["aliases"] = payload.Aliases
	}
	if len(payload.PrimaryPath) > 0 {
		frontmatter["primary_path"] = payload.PrimaryPath
	}
	if len(payload.Classifications) > 0 {
		frontmatter["classifications"] = payload.Classifications
	}
	if len(payload.Tags) > 0 {
		frontmatter["tags"] = payload.Tags
	}
	if len(payload.Sources) > 0 {
		frontmatter["sources"] = payload.Sources
	}
	if len(payload.Relations) > 0 {
		frontmatter["relations"] = payload.Relations
	}
	if payload.Generated.By != "" {
		frontmatter["generated"] = payload.Generated
	}
	if len(payload.Verified) > 0 {
		frontmatter["verified"] = payload.Verified
	}
	if payload.StaleAfter != nil {
		frontmatter["stale_after"] = payload.StaleAfter.Format(time.RFC3339)
	}
	bytes, err := yaml.Marshal(frontmatter)
	if err != nil {
		return "", err
	}
	var body strings.Builder
	body.WriteString("---\n")
	body.Write(bytes)
	body.WriteString("---\n\n# ")
	body.WriteString(payload.Title)
	body.WriteString("\n")
	for _, section := range payload.Sections {
		body.WriteString("\n## ")
		body.WriteString(section.Heading)
		body.WriteString(" {#")
		body.WriteString(section.ID)
		body.WriteString("}\n\n")
		body.WriteString(section.Content)
		body.WriteString("\n")
	}
	return body.String(), nil
}

func (s *Store) findNearKnowledge(ctx context.Context, libraryID, title, excludeID string) ([]model.KnowledgeDirectoryEntry, error) {
	needle := strings.ToLower(strings.TrimSpace(title))
	if needle == "" {
		return []model.KnowledgeDirectoryEntry{}, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT ki.id,kr.revision,kr.payload_json,kr.flags_json FROM knowledge_items ki JOIN knowledge_revisions kr ON kr.knowledge_id=ki.id AND kr.revision=ki.stable_revision WHERE ki.library_id=? AND ki.id<>?`, libraryID, excludeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.KnowledgeDirectoryEntry{}
	for rows.Next() {
		var id, payloadJSON, flagsJSON string
		var revision int
		if err := rows.Scan(&id, &revision, &payloadJSON, &flagsJSON); err != nil {
			return nil, err
		}
		var payload model.KnowledgePayload
		var flags []string
		_ = json.Unmarshal([]byte(payloadJSON), &payload)
		_ = json.Unmarshal([]byte(flagsJSON), &flags)
		current := strings.ToLower(payload.Title)
		if current == needle || strings.Contains(current, needle) || strings.Contains(needle, current) {
			items = append(items, directoryEntry(payload, revision, flags, nil, "similar title"))
		}
	}
	return items, rows.Err()
}

func directoryEntry(payload model.KnowledgePayload, revision int, flags []string, matched []string, reason string) model.KnowledgeDirectoryEntry {
	if payload.PrimaryPath == nil {
		payload.PrimaryPath = []string{}
	}
	if payload.Tags == nil {
		payload.Tags = []string{}
	}
	if flags == nil {
		flags = []string{}
	}
	if matched == nil {
		matched = []string{}
	}
	trust := "unverified"
	if len(payload.Verified) > 0 {
		trust = "verified"
	}
	return model.KnowledgeDirectoryEntry{URI: payload.ID, Revision: revision, Title: payload.Title, Description: payload.Description, Type: payload.Type, Subtype: payload.Subtype, Language: payload.Language, PrimaryPath: payload.PrimaryPath, Classifications: payload.Classifications, Tags: payload.Tags, MatchedSectionIDs: matched, MatchReason: reason, Flags: flags, Trust: trust}
}

func (s *Store) GetKnowledge(ctx context.Context, uri string, allowDraft bool) (model.KnowledgeRevision, error) {
	id, requestedRevision, _, err := ParseKnowledgeURI(uri)
	if err != nil {
		return model.KnowledgeRevision{}, err
	}
	var libraryID, payloadJSON, markdown, contentHash, status, flagsJSON, submittedBy, created string
	var revision int
	var stable sql.NullInt64
	query := `SELECT ki.library_id,ki.stable_revision,kr.revision,kr.payload_json,kr.markdown,kr.content_hash,kr.status,kr.flags_json,kr.submitted_by,kr.created_at FROM knowledge_items ki JOIN knowledge_revisions kr ON kr.knowledge_id=ki.id WHERE ki.id=?`
	args := []any{id}
	if requestedRevision > 0 {
		query += ` AND kr.revision=?`
		args = append(args, requestedRevision)
	}
	if !allowDraft && requestedRevision == 0 {
		query += ` AND kr.revision=ki.stable_revision`
	} else if !allowDraft {
		// Explicit historical revisions remain readable for reproducible
		// citations, but drafts and rejected revisions never cross the MCP read
		// boundary.
		query += ` AND kr.status IN ('stable','deprecated')`
	} else if requestedRevision == 0 {
		query += ` ORDER BY kr.revision DESC LIMIT 1`
	}
	err = s.DB.QueryRowContext(ctx, query, args...).Scan(&libraryID, &stable, &revision, &payloadJSON, &markdown, &contentHash, &status, &flagsJSON, &submittedBy, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return model.KnowledgeRevision{}, ErrRevisionNotFound
	}
	if err != nil {
		return model.KnowledgeRevision{}, err
	}
	if !allowDraft && status != "stable" && status != "deprecated" {
		return model.KnowledgeRevision{}, ErrRevisionNotFound
	}
	var payload model.KnowledgePayload
	var flags []string
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return model.KnowledgeRevision{}, fmt.Errorf("decode knowledge payload: %w", err)
	}
	if err := json.Unmarshal([]byte(flagsJSON), &flags); err != nil {
		return model.KnowledgeRevision{}, fmt.Errorf("decode knowledge flags: %w", err)
	}
	if flags == nil {
		flags = []string{}
	}
	if payload.ID == "" {
		payload.ID = KnowledgeURI(id)
	}
	return model.KnowledgeRevision{LibraryID: libraryID, KnowledgeID: id, URI: payload.ID, Revision: revision, Payload: payload, Markdown: markdown, ContentHash: contentHash, Status: status, Flags: flags, Stable: stable.Valid && int(stable.Int64) == revision, SubmittedBy: submittedBy, CreatedAt: parseTime(created)}, nil
}

func (s *Store) SearchKnowledge(ctx context.Context, request model.KnowledgeSearchRequest) (model.KnowledgeSearchResponse, error) {
	if request.Limit < 1 || request.Limit > 100 {
		request.Limit = 20
	}
	query := `SELECT ki.library_id,kr.revision,kr.payload_json,kr.flags_json FROM knowledge_items ki JOIN knowledge_revisions kr ON kr.knowledge_id=ki.id AND kr.revision=ki.stable_revision WHERE kr.status='stable'`
	args := []any{}
	if len(request.Statuses) > 0 && !containsText(request.Statuses, "stable") {
		return model.KnowledgeSearchResponse{Results: []model.KnowledgeDirectoryEntry{}}, nil
	}
	if len(request.LibraryIDs) > 0 {
		query += ` AND ki.library_id IN (` + strings.TrimRight(strings.Repeat("?,", len(request.LibraryIDs)), ",") + `)`
		for _, id := range request.LibraryIDs {
			args = append(args, id)
		}
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return model.KnowledgeSearchResponse{}, err
	}
	defer rows.Close()
	type ranked struct {
		entry model.KnowledgeDirectoryEntry
		score int
	}
	rankedItems := []ranked{}
	needle := strings.ToLower(strings.TrimSpace(request.Query))
	for rows.Next() {
		var libraryID, payloadJSON, flagsJSON string
		var revision int
		if err := rows.Scan(&libraryID, &revision, &payloadJSON, &flagsJSON); err != nil {
			return model.KnowledgeSearchResponse{}, err
		}
		var payload model.KnowledgePayload
		var flags []string
		_ = json.Unmarshal([]byte(payloadJSON), &payload)
		_ = json.Unmarshal([]byte(flagsJSON), &flags)
		if request.Language != "" && request.Language != payload.Language {
			continue
		}
		if len(request.Types) > 0 && !containsText(request.Types, payload.Type) {
			continue
		}
		if len(request.Tags) > 0 && !overlaps(request.Tags, payload.Tags) {
			continue
		}
		if !matchesClassifications(request.Classifications, payload.Classifications) {
			continue
		}
		score := 0
		matched := []string{}
		if needle == "" {
			score = 1
		} else {
			if strings.Contains(strings.ToLower(payload.Title), needle) {
				score += 12
			}
			if strings.Contains(strings.ToLower(payload.Description), needle) {
				score += 5
			}
			for _, alias := range payload.Aliases {
				if strings.Contains(strings.ToLower(alias), needle) {
					score += 4
				}
			}
			for _, section := range payload.Sections {
				if strings.Contains(strings.ToLower(section.Content), needle) {
					score += 3
					matched = append(matched, section.ID)
				}
			}
		}
		if score == 0 {
			continue
		}
		if containsText(flags, "stale") || containsText(flags, "disputed") {
			score--
		}
		entry := directoryEntry(payload, revision, flags, matched, "title, summary, alias, or section match")
		for _, relation := range payload.Relations {
			if relation.Type == "contradicts" {
				entry.ConflictURIs = append(entry.ConflictURIs, relation.Target)
			}
		}
		rankedItems = append(rankedItems, ranked{entry, score})
	}
	if err := rows.Err(); err != nil {
		return model.KnowledgeSearchResponse{}, err
	}
	sort.SliceStable(rankedItems, func(i, j int) bool {
		if rankedItems[i].score == rankedItems[j].score {
			return rankedItems[i].entry.URI < rankedItems[j].entry.URI
		}
		return rankedItems[i].score > rankedItems[j].score
	})
	start := 0
	if request.Cursor != "" {
		cursor, err := decodeKnowledgeCursor(request.Cursor)
		if err != nil {
			return model.KnowledgeSearchResponse{}, err
		}
		start = len(rankedItems)
		for index, item := range rankedItems {
			if item.score < cursor.Score || (item.score == cursor.Score && item.entry.URI > cursor.URI) {
				start = index
				break
			}
		}
	}
	result := model.KnowledgeSearchResponse{Results: []model.KnowledgeDirectoryEntry{}}
	for _, item := range rankedItems[start:] {
		if len(result.Results) >= request.Limit {
			break
		}
		result.Results = append(result.Results, item.entry)
	}
	if len(result.Results) > 0 && start+len(result.Results) < len(rankedItems) {
		last := rankedItems[start+len(result.Results)-1]
		result.NextCursor = encodeKnowledgeCursor(knowledgeCursor{Score: last.score, URI: last.entry.URI})
	}
	return result, nil
}

type knowledgeCursor struct {
	Score int    `json:"score"`
	URI   string `json:"uri"`
}

func encodeKnowledgeCursor(value knowledgeCursor) string {
	bytes, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(bytes)
}

func decodeKnowledgeCursor(value string) (knowledgeCursor, error) {
	bytes, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return knowledgeCursor{}, errors.New("invalid knowledge cursor")
	}
	var cursor knowledgeCursor
	if err := json.Unmarshal(bytes, &cursor); err != nil || cursor.URI == "" {
		return knowledgeCursor{}, errors.New("invalid knowledge cursor")
	}
	return cursor, nil
}

func matchesClassifications(requested, actual map[string][]string) bool {
	for key, values := range requested {
		if len(values) == 0 || !overlaps(values, actual[key]) {
			return false
		}
	}
	return true
}

func containsText(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func overlaps(left, right []string) bool {
	for _, value := range left {
		if containsText(right, value) {
			return true
		}
	}
	return false
}

func (s *Store) GetKAHSubmission(ctx context.Context, id string) (model.KAHSubmission, error) {
	var item model.KAHSubmission
	var validationJSON, created, updated string
	err := s.DB.QueryRowContext(ctx, `SELECT id,library_id,knowledge_id,revision,mode,review_status,validation_json,submitted_by_token_id,client_submission_id,created_at,updated_at FROM kah_submissions WHERE id=?`, id).Scan(&item.ID, &item.LibraryID, &item.KnowledgeURI, &item.Revision, &item.Mode, &item.ReviewStatus, &validationJSON, &item.SubmittedByTokenID, &item.ClientSubmissionID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return item, sql.ErrNoRows
	}
	if err != nil {
		return item, err
	}
	item.KnowledgeURI = KnowledgeURI(item.KnowledgeURI)
	if err := json.Unmarshal([]byte(validationJSON), &item.Validation); err != nil {
		return item, fmt.Errorf("decode submission validation: %w", err)
	}
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	revision, revisionErr := s.GetKnowledge(ctx, item.KnowledgeURI+"?revision="+fmt.Sprint(item.Revision), true)
	if revisionErr != nil {
		return item, fmt.Errorf("load submission revision: %w", revisionErr)
	}
	item.Title = revision.Payload.Title
	item.Summary = revision.Payload.Description
	item.Tags = revision.Payload.Tags
	if item.Tags == nil {
		item.Tags = []string{}
	}
	item.Markdown = revision.Markdown
	item.Provenance = map[string]any{"generated": revision.Payload.Generated, "sources": revision.Payload.Sources, "flags": revision.Flags}
	item.Reviews = []model.KAHReviewRecord{}
	reviews, reviewsErr := s.DB.QueryContext(ctx, `SELECT id,submission_id,reviewer_type,reviewer,decision,reason,created_at FROM kah_reviews WHERE submission_id=? ORDER BY created_at`, item.ID)
	if reviewsErr != nil {
		return item, reviewsErr
	}
	defer reviews.Close()
	for reviews.Next() {
		var review model.KAHReviewRecord
		var createdAt string
		if err := reviews.Scan(&review.ID, &review.SubmissionID, &review.ReviewerType, &review.Reviewer, &review.Decision, &review.Reason, &createdAt); err != nil {
			return item, err
		}
		review.CreatedAt = parseTime(createdAt)
		item.Reviews = append(item.Reviews, review)
	}
	if err := reviews.Err(); err != nil {
		return item, err
	}
	return item, nil
}

func (s *Store) ListKAHSubmissions(ctx context.Context, libraryID string) ([]model.KAHSubmission, error) {
	query := `SELECT id FROM kah_submissions`
	args := []any{}
	if libraryID != "" {
		query += ` WHERE library_id=?`
		args = append(args, libraryID)
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	items := make([]model.KAHSubmission, 0, len(ids))
	for _, id := range ids {
		item, err := s.GetKAHSubmission(ctx, id)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) ReviewKAHSubmission(ctx context.Context, id, reviewer, decision, reason string) (model.KAHSubmission, error) {
	if decision != "approve" && decision != "reject" {
		return model.KAHSubmission{}, errors.New("decision must be approve or reject")
	}
	if decision == "reject" && strings.TrimSpace(reason) == "" {
		return model.KAHSubmission{}, errors.New("rejection reason is required")
	}
	item, err := s.GetKAHSubmission(ctx, id)
	if err != nil {
		return item, err
	}
	if item.ReviewStatus != "pending_review" {
		return item, errors.New("submission is no longer pending review")
	}
	_, stamp := now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return item, err
	}
	defer tx.Rollback()
	status := "rejected"
	revisionStatus := "draft"
	if decision == "approve" {
		status = "approved_pending_index"
		revisionStatus = "approved_pending_index"
	}
	updated, err := tx.ExecContext(ctx, `UPDATE kah_submissions SET review_status=?,updated_at=? WHERE id=? AND review_status='pending_review'`, status, stamp, id)
	if err != nil {
		return item, err
	}
	if affected, affectedErr := updated.RowsAffected(); affectedErr != nil {
		return item, affectedErr
	} else if affected != 1 {
		return item, errors.New("submission is no longer pending review")
	}
	knowledgeID, _, _, _ := ParseKnowledgeURI(item.KnowledgeURI)
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_revisions SET status=? WHERE knowledge_id=? AND revision=?`, revisionStatus, knowledgeID, item.Revision); err != nil {
		return item, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO kah_reviews(id,submission_id,reviewer_type,reviewer,decision,reason,created_at) VALUES(?,?, 'human',?,?,?,?)`, uuid.NewString(), id, reviewer, decision, strings.TrimSpace(reason), stamp); err != nil {
		return item, err
	}
	if err = tx.Commit(); err != nil {
		return item, err
	}
	return s.GetKAHSubmission(ctx, id)
}

func (s *Store) PublishKAHSubmission(ctx context.Context, id string) (model.KnowledgeRevision, error) {
	item, err := s.GetKAHSubmission(ctx, id)
	if err != nil {
		return model.KnowledgeRevision{}, err
	}
	if item.ReviewStatus == "published" {
		return s.GetKnowledge(ctx, item.KnowledgeURI+"?revision="+fmt.Sprint(item.Revision), false)
	}
	if item.ReviewStatus != "approved_pending_index" {
		return model.KnowledgeRevision{}, errors.New("submission is not approved")
	}
	knowledgeID, _, _, _ := ParseKnowledgeURI(item.KnowledgeURI)
	revision, err := s.GetKnowledge(ctx, item.KnowledgeURI+"?revision="+fmt.Sprint(item.Revision), true)
	if err != nil {
		return model.KnowledgeRevision{}, err
	}
	if containsText(revision.Flags, "source-unverified") {
		return model.KnowledgeRevision{}, errors.New("SOURCE_UNVERIFIED")
	}
	_, stamp := now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.KnowledgeRevision{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_revisions SET status='deprecated' WHERE knowledge_id=? AND revision<>? AND status='stable'`, knowledgeID, item.Revision); err != nil {
		return model.KnowledgeRevision{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_revisions SET status='stable' WHERE knowledge_id=? AND revision=?`, knowledgeID, item.Revision); err != nil {
		return model.KnowledgeRevision{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_items SET stable_revision=?,updated_at=? WHERE id=?`, item.Revision, stamp, knowledgeID); err != nil {
		return model.KnowledgeRevision{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE kah_submissions SET review_status='published',updated_at=? WHERE id=?`, stamp, id); err != nil {
		return model.KnowledgeRevision{}, err
	}
	if err = tx.Commit(); err != nil {
		return model.KnowledgeRevision{}, err
	}
	return s.GetKnowledge(ctx, item.KnowledgeURI+"?revision="+fmt.Sprint(item.Revision), true)
}

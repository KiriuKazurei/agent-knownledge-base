package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/secrets"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const (
	submissionMaxBytes  = 1 << 20
	submissionTicketTTL = 15 * time.Minute
)

type submissionSource struct {
	Label string
	Ref   string
	Note  string
}

type submissionProvenance struct {
	Type    string
	Basis   string
	Sources []submissionSource
}

type submissionFrontmatter struct {
	Title      string
	Summary    string
	Tags       []string
	Language   string
	Provenance submissionProvenance
}

type parsedSubmissionMarkdown struct {
	Markdown   string
	Body       string
	Title      string
	Summary    string
	Tags       []string
	Provenance map[string]any
}

func normalizeSubmissionMarkdown(value string) (parsedSubmissionMarkdown, error) {
	if !utf8.ValidString(value) {
		return parsedSubmissionMarkdown{}, errors.New("markdown must be valid UTF-8")
	}
	value = strings.TrimPrefix(value, "\uFEFF")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if len([]byte(value)) == 0 || len([]byte(value)) > submissionMaxBytes {
		return parsedSubmissionMarkdown{}, errors.New("markdown size is invalid")
	}
	if strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\uFFFD') {
		return parsedSubmissionMarkdown{}, errors.New("markdown contains invalid replacement characters")
	}
	lines := strings.Split(value, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return parsedSubmissionMarkdown{}, errors.New("YAML frontmatter is required")
	}
	closeIndex := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closeIndex = i
			break
		}
	}
	if closeIndex < 0 {
		return parsedSubmissionMarkdown{}, errors.New("YAML frontmatter is not closed")
	}
	frontmatterBytes := []byte(strings.Join(lines[1:closeIndex], "\n"))
	var fields map[string]any
	if err := yaml.Unmarshal(frontmatterBytes, &fields); err != nil {
		return parsedSubmissionMarkdown{}, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	for _, required := range []string{"title", "summary", "tags", "language", "provenance"} {
		if _, ok := fields[required]; !ok {
			return parsedSubmissionMarkdown{}, fmt.Errorf("frontmatter field %q is required", required)
		}
	}
	var frontmatter submissionFrontmatter
	if err := yaml.Unmarshal(frontmatterBytes, &frontmatter); err != nil {
		return parsedSubmissionMarkdown{}, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	frontmatter.Title = strings.TrimSpace(frontmatter.Title)
	frontmatter.Summary = strings.TrimSpace(frontmatter.Summary)
	frontmatter.Language = strings.TrimSpace(frontmatter.Language)
	if frontmatter.Title == "" || len([]rune(frontmatter.Title)) > 200 {
		return parsedSubmissionMarkdown{}, errors.New("frontmatter title is required and must be at most 200 characters")
	}
	if frontmatter.Summary == "" || len([]rune(frontmatter.Summary)) > 1000 {
		return parsedSubmissionMarkdown{}, errors.New("frontmatter summary is required and must be at most 1000 characters")
	}
	if frontmatter.Language == "" || len([]rune(frontmatter.Language)) > 50 {
		return parsedSubmissionMarkdown{}, errors.New("frontmatter language is required")
	}
	if len(frontmatter.Tags) > 20 {
		return parsedSubmissionMarkdown{}, errors.New("at most 20 tags are allowed")
	}
	for i, tag := range frontmatter.Tags {
		frontmatter.Tags[i] = strings.TrimSpace(tag)
		if frontmatter.Tags[i] == "" || len([]rune(frontmatter.Tags[i])) > 50 {
			return parsedSubmissionMarkdown{}, errors.New("tags must be non-empty and at most 50 characters")
		}
	}
	frontmatter.Provenance.Type = strings.TrimSpace(frontmatter.Provenance.Type)
	frontmatter.Provenance.Basis = strings.TrimSpace(frontmatter.Provenance.Basis)
	switch frontmatter.Provenance.Type {
	case "external":
		if len(frontmatter.Provenance.Sources) == 0 {
			return parsedSubmissionMarkdown{}, errors.New("external provenance requires at least one source")
		}
		for _, source := range frontmatter.Provenance.Sources {
			if strings.TrimSpace(source.Ref) == "" {
				return parsedSubmissionMarkdown{}, errors.New("each provenance source requires ref")
			}
		}
	case "internal", "agent_observation":
		if frontmatter.Provenance.Basis == "" {
			return parsedSubmissionMarkdown{}, errors.New("internal or agent_observation provenance requires basis")
		}
	default:
		return parsedSubmissionMarkdown{}, errors.New("provenance type must be external, internal, or agent_observation")
	}
	body := strings.TrimSpace(strings.Join(lines[closeIndex+1:], "\n"))
	if body == "" {
		return parsedSubmissionMarkdown{}, errors.New("markdown body is required")
	}
	h1Count := 0
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			h1Count++
			if strings.TrimSpace(strings.TrimPrefix(trimmed, "# ")) != frontmatter.Title {
				return parsedSubmissionMarkdown{}, errors.New("the H1 heading must exactly match frontmatter title")
			}
		}
	}
	if h1Count != 1 {
		return parsedSubmissionMarkdown{}, errors.New("exactly one H1 heading is required")
	}
	for _, heading := range []string{"核心内容", "适用范围", "限制与不确定性"} {
		if !submissionSectionHasContent(body, heading) {
			return parsedSubmissionMarkdown{}, fmt.Errorf("required section %q is missing or empty", heading)
		}
	}
	provenance := map[string]any{"type": frontmatter.Provenance.Type}
	if frontmatter.Provenance.Basis != "" {
		provenance["basis"] = frontmatter.Provenance.Basis
	}
	if len(frontmatter.Provenance.Sources) > 0 {
		sources := make([]map[string]any, 0, len(frontmatter.Provenance.Sources))
		for _, source := range frontmatter.Provenance.Sources {
			sources = append(sources, map[string]any{"label": source.Label, "ref": source.Ref, "note": source.Note})
		}
		provenance["sources"] = sources
	}
	return parsedSubmissionMarkdown{
		Markdown:   bodyWithFrontmatter(frontmatter, body),
		Body:       body,
		Title:      frontmatter.Title,
		Summary:    frontmatter.Summary,
		Tags:       frontmatter.Tags,
		Provenance: provenance,
	}, nil
}

func bodyWithFrontmatter(frontmatter submissionFrontmatter, body string) string {
	encoded, _ := yaml.Marshal(frontmatter)
	return "---\n" + string(encoded) + "---\n\n" + body + "\n"
}

func submissionSectionHasContent(body, heading string) bool {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "## "+heading {
			continue
		}
		for _, candidate := range lines[i+1:] {
			trimmed := strings.TrimSpace(candidate)
			if strings.HasPrefix(trimmed, "## ") {
				return false
			}
			if trimmed != "" {
				return true
			}
		}
		return false
	}
	return false
}

func (s *Server) prepareKnowledgeSubmission(c *gin.Context) {
	libraryID := strings.TrimSpace(c.Query("libraryId"))
	if libraryID == "" {
		var input struct{ LibraryID string }
		if !bind(c, &input) {
			return
		}
		libraryID = strings.TrimSpace(input.LibraryID)
	}
	if !s.authorizeSubmissionLibrary(c, libraryID) {
		return
	}
	library, ok, err := s.findLibrary(operationContext(c), libraryID)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	if !ok {
		s.problem(c, http.StatusBadRequest, "library_not_found", "libraryId was not found", false)
		return
	}
	formatter, err := s.Store.GetSystemSkill(operationContext(c), storage.SubmissionFormatterRole)
	if err != nil {
		s.problem(c, http.StatusServiceUnavailable, "formatter_unavailable", "submission formatter Skill is unavailable", true)
		return
	}
	path, _, err := s.Store.ReadSkillFile(operationContext(c), formatter.ID, "SKILL.md")
	if err != nil {
		s.problem(c, http.StatusServiceUnavailable, "formatter_unavailable", "submission formatter Skill file is unavailable", true)
		return
	}
	content, err := os.ReadFile(path)
	if err != nil {
		s.problem(c, http.StatusServiceUnavailable, "formatter_unavailable", "submission formatter Skill file cannot be read", true)
		return
	}
	value, _ := c.Get("identity")
	id := value.(identity)
	expiry := time.Now().UTC().Add(submissionTicketTTL)
	ticket, err := s.Store.CreateSubmissionTicket(operationContext(c), id.Token.ID, library.ID, formatter.ID, formatter.ContentHash, expiry)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "submission_ticket_failed", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, model.SubmissionPreparation{
		Ticket:      ticket,
		ExpiresAt:   expiry,
		Formatter:   model.SubmissionFormatter{ID: formatter.ID, Name: formatter.Name, Description: formatter.Description, ContentHash: formatter.ContentHash, Content: string(content), Files: []string{"SKILL.md"}},
		Constraints: model.SubmissionConstraints{MaxBytes: submissionMaxBytes, RequiredFrontmatter: []string{"title", "summary", "tags", "language", "provenance"}, RequiredSections: []string{"核心内容", "适用范围", "限制与不确定性"}},
	})
}

func (s *Server) submitKnowledgeSubmission(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, submissionMaxBytes+256*1024)
	var input struct {
		LibraryID              string
		Ticket                 string
		ClientSubmissionID     string
		Markdown               string
		SupersedesSubmissionID string
	}
	if !bind(c, &input) {
		return
	}
	input.LibraryID = strings.TrimSpace(input.LibraryID)
	input.Ticket = strings.TrimSpace(input.Ticket)
	input.ClientSubmissionID = strings.TrimSpace(input.ClientSubmissionID)
	if input.LibraryID == "" || input.Ticket == "" || input.ClientSubmissionID == "" || input.Markdown == "" {
		s.problem(c, http.StatusBadRequest, "invalid_submission", "libraryId, ticket, clientSubmissionId, and markdown are required", false)
		return
	}
	if len([]rune(input.ClientSubmissionID)) > 200 {
		s.problem(c, http.StatusBadRequest, "invalid_submission", "clientSubmissionId is too long", false)
		return
	}
	if !s.authorizeSubmissionLibrary(c, input.LibraryID) {
		return
	}
	parsed, err := normalizeSubmissionMarkdown(input.Markdown)
	if err != nil {
		s.problem(c, http.StatusBadRequest, "invalid_submission_markdown", err.Error(), false)
		return
	}
	value, _ := c.Get("identity")
	id := value.(identity)
	formatter, err := s.Store.GetSystemSkill(operationContext(c), storage.SubmissionFormatterRole)
	if err != nil {
		s.problem(c, http.StatusServiceUnavailable, "formatter_unavailable", "submission formatter Skill is unavailable", true)
		return
	}
	library, ok, err := s.findLibrary(operationContext(c), input.LibraryID)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	if !ok {
		s.problem(c, http.StatusBadRequest, "library_not_found", "libraryId was not found", false)
		return
	}
	objectPath, digest, err := s.Store.PutObject(strings.NewReader(parsed.Markdown))
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "submission_storage_failed", err.Error(), true)
		return
	}
	document := model.Document{ID: uuid.NewString(), LibraryID: input.LibraryID, Title: parsed.Title, MediaType: "text/markdown", SourcePath: parsed.Title + ".md", ObjectPath: objectPath, ContentHash: digest, Status: "pending_review", Tags: parsed.Tags}
	var chunks []model.Chunk
	if s.Worker == nil {
		chunks = fallbackChunks(document.ID, parsed.Body, map[string]any{"kind": "markdown"})
	} else {
		chunks, err = s.parseDocument(operationContext(c), document, mustResolve(s.Store, objectPath))
		if err != nil {
			s.problem(c, http.StatusBadRequest, "submission_parse_failed", err.Error(), false)
			return
		}
	}
	submission, idempotent, err := s.Store.CreateKnowledgeSubmission(operationContext(c), storage.SubmissionCreateInput{TokenID: id.Token.ID, LibraryID: input.LibraryID, ClientSubmissionID: input.ClientSubmissionID, Ticket: input.Ticket, FormatterSkillID: formatter.ID, FormatterSkillHash: formatter.ContentHash, SupersedesSubmissionID: strings.TrimSpace(input.SupersedesSubmissionID), Document: document, Chunks: chunks, Summary: parsed.Summary, Tags: parsed.Tags, Provenance: parsed.Provenance})
	if err != nil {
		s.writeSubmissionError(c, err)
		return
	}
	if !idempotent && library.AutoReviewAgentSubmissions {
		job, jobErr := s.Store.CreateJob(operationContext(c), "knowledge_review", map[string]any{"submissionId": submission.ID})
		if jobErr != nil {
			_ = s.Store.SetSubmissionReviewError(operationContext(c), submission.ID, jobErr)
			s.problem(c, http.StatusInternalServerError, "review_job_failed", jobErr.Error(), true)
			return
		}
		_ = s.Store.SetSubmissionReviewJob(operationContext(c), submission.ID, job.ID)
		go s.runKnowledgeReview(job.ID, submission.ID)
		submission.ReviewJobID = job.ID
	}
	status := http.StatusCreated
	if idempotent {
		status = http.StatusOK
	}
	c.JSON(status, submission)
}

func mustResolve(store *storage.Store, relative string) string {
	path, err := store.Resolve(relative)
	if err != nil {
		return relative
	}
	return path
}

func (s *Server) listKnowledgeSubmissions(c *gin.Context) {
	value, _ := c.Get("identity")
	id := value.(identity)
	ownOnly := !id.Desktop
	libraryID := strings.TrimSpace(c.Query("libraryId"))
	if libraryID != "" && !s.authorizeSubmissionLibrary(c, libraryID) {
		return
	}
	items, err := s.Store.ListKnowledgeSubmissions(operationContext(c), id.Token.ID, libraryID, strings.TrimSpace(c.Query("status")), ownOnly)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) getKnowledgeSubmission(c *gin.Context) {
	item, err := s.authorizedSubmission(c, c.Param("id"))
	if err != nil {
		s.writeSubmissionError(c, err)
		return
	}
	item, _, err = s.Store.GetKnowledgeSubmission(operationContext(c), item.ID, true)
	if err != nil {
		s.writeSubmissionError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) approveKnowledgeSubmission(c *gin.Context) {
	var input struct{ Reason string }
	if c.Request.ContentLength > 0 && !bind(c, &input) {
		return
	}
	item, err := s.loadReviewTarget(c)
	if err != nil {
		s.writeSubmissionError(c, err)
		return
	}
	if hasModelRejection(item) && strings.TrimSpace(input.Reason) == "" {
		s.problem(c, http.StatusBadRequest, "review_reason_required", "reason is required to override a model rejection", false)
		return
	}
	changed, err := s.Store.RecordSubmissionReview(operationContext(c), item.ID, "human", actorName(c), "approve", 1, strings.TrimSpace(input.Reason), nil)
	if err != nil {
		s.writeSubmissionError(c, err)
		return
	}
	if !changed {
		s.problem(c, http.StatusConflict, "submission_state_conflict", "submission is no longer awaiting review", false)
		return
	}
	s.queueSubmissionPublish(c, item.ID)
}

func (s *Server) rejectKnowledgeSubmission(c *gin.Context) {
	var input struct{ Reason string }
	if !bind(c, &input) || strings.TrimSpace(input.Reason) == "" {
		if !c.IsAborted() {
			s.problem(c, http.StatusBadRequest, "review_reason_required", "reason is required when rejecting a submission", false)
		}
		return
	}
	item, err := s.loadReviewTarget(c)
	if err != nil {
		s.writeSubmissionError(c, err)
		return
	}
	changed, err := s.Store.RecordSubmissionReview(operationContext(c), item.ID, "human", actorName(c), "reject", 1, strings.TrimSpace(input.Reason), nil)
	if err != nil {
		s.writeSubmissionError(c, err)
		return
	}
	if !changed {
		s.problem(c, http.StatusConflict, "submission_state_conflict", "submission is no longer awaiting review", false)
		return
	}
	updated, _, getErr := s.Store.GetKnowledgeSubmission(operationContext(c), item.ID, false)
	if getErr != nil {
		s.writeSubmissionError(c, getErr)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (s *Server) retryKnowledgeSubmissionReview(c *gin.Context) {
	item, err := s.loadReviewTarget(c)
	if err != nil {
		s.writeSubmissionError(c, err)
		return
	}
	if item.Status == "approved_pending_index" {
		job, createErr := s.Store.CreateJob(operationContext(c), "knowledge_publish", map[string]any{"submissionId": item.ID})
		if createErr != nil {
			_ = s.Store.SetSubmissionPublishError(operationContext(c), item.ID, createErr)
			s.problem(c, http.StatusInternalServerError, "publish_job_failed", createErr.Error(), true)
			return
		}
		go s.runKnowledgePublish(job.ID, item.ID)
		c.JSON(http.StatusAccepted, job)
		return
	}
	library, ok, err := s.findLibrary(operationContext(c), item.LibraryID)
	if err != nil || !ok {
		s.problem(c, http.StatusBadRequest, "library_not_found", "library was not found", false)
		return
	}
	if !library.AutoReviewAgentSubmissions || library.ReviewProviderID == "" {
		s.problem(c, http.StatusBadRequest, "auto_review_not_configured", "automatic review is not configured", false)
		return
	}
	if err := s.Store.ResetSubmissionReview(operationContext(c), item.ID); err != nil {
		s.writeSubmissionError(c, err)
		return
	}
	job, err := s.Store.CreateJob(operationContext(c), "knowledge_review", map[string]any{"submissionId": item.ID})
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "review_job_failed", err.Error(), true)
		return
	}
	_ = s.Store.SetSubmissionReviewJob(operationContext(c), item.ID, job.ID)
	go s.runKnowledgeReview(job.ID, item.ID)
	c.JSON(http.StatusAccepted, job)
}

func (s *Server) loadReviewTarget(c *gin.Context) (model.KnowledgeSubmission, error) {
	return s.authorizedSubmission(c, c.Param("id"))
}

func (s *Server) authorizedSubmission(c *gin.Context, submissionID string) (model.KnowledgeSubmission, error) {
	value, _ := c.Get("identity")
	id := value.(identity)
	item, _, err := s.Store.GetKnowledgeSubmission(operationContext(c), submissionID, false)
	if err != nil {
		return model.KnowledgeSubmission{}, err
	}
	if !id.Desktop && !contains(id.Token.LibraryIDs, item.LibraryID) {
		return model.KnowledgeSubmission{}, errors.New("submission is not accessible to this token")
	}
	if !id.Desktop {
		owner, _, ownerErr := s.Store.KnowledgeSubmissionOwner(operationContext(c), submissionID)
		if ownerErr != nil || owner != id.Token.ID {
			return model.KnowledgeSubmission{}, errors.New("submission is not owned by this token")
		}
	}
	return item, nil
}

func (s *Server) authorizeSubmissionLibrary(c *gin.Context, libraryID string) bool {
	if libraryID == "" {
		s.problem(c, http.StatusBadRequest, "library_required", "libraryId is required", false)
		return false
	}
	value, _ := c.Get("identity")
	id := value.(identity)
	if id.Desktop {
		return true
	}
	if !contains(id.Token.LibraryIDs, libraryID) {
		s.problem(c, http.StatusForbidden, "library_forbidden", "Token cannot submit to this library", false)
		return false
	}
	return true
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func (s *Server) findLibrary(ctx context.Context, id string) (model.Library, bool, error) {
	items, err := s.Store.ListLibraries(ctx)
	if err != nil {
		return model.Library{}, false, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, true, nil
		}
	}
	return model.Library{}, false, nil
}

func hasModelRejection(item model.KnowledgeSubmission) bool {
	for _, review := range item.Reviews {
		if review.ReviewerType == "model" && review.Decision == "reject" {
			return true
		}
	}
	return false
}

func (s *Server) queueSubmissionPublish(c *gin.Context, submissionID string) {
	job, err := s.Store.CreateJob(operationContext(c), "knowledge_publish", map[string]any{"submissionId": submissionID})
	if err != nil {
		_ = s.Store.SetSubmissionPublishError(operationContext(c), submissionID, err)
		s.problem(c, http.StatusInternalServerError, "publish_job_failed", err.Error(), true)
		return
	}
	go s.runKnowledgePublish(job.ID, submissionID)
	c.JSON(http.StatusAccepted, job)
}

func (s *Server) writeSubmissionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, storage.ErrSubmissionNotFound):
		s.problem(c, http.StatusNotFound, "submission_not_found", "Knowledge submission was not found", false)
	case errors.Is(err, storage.ErrSubmissionTicketInvalid):
		s.problem(c, http.StatusUnauthorized, "invalid_submission_ticket", "Submission ticket is invalid for this token, library, or formatter version", false)
	case errors.Is(err, storage.ErrSubmissionTicketExpired):
		s.problem(c, http.StatusGone, "submission_ticket_expired", "Submission ticket has expired", false)
	case errors.Is(err, storage.ErrSubmissionTicketUsed):
		s.problem(c, http.StatusConflict, "submission_ticket_used", "Submission ticket has already been used", false)
	case errors.Is(err, storage.ErrSubmissionDuplicate):
		s.problem(c, http.StatusConflict, "submission_duplicate", err.Error(), false)
	case errors.Is(err, storage.ErrSubmissionConflict):
		s.problem(c, http.StatusConflict, "submission_revision_conflict", "Only a rejected submission from the same token can be superseded", false)
	case strings.Contains(err.Error(), "not accessible"), strings.Contains(err.Error(), "not owned"):
		s.problem(c, http.StatusForbidden, "submission_forbidden", "Submission is not accessible to this identity", false)
	default:
		s.problem(c, http.StatusInternalServerError, "submission_failed", err.Error(), true)
	}
}

type reviewModelIssue struct {
	Code     string
	Severity string
	Message  string
}

type reviewModelOutput struct {
	Decision   string
	Confidence float64
	Reason     string
	Issues     []reviewModelIssue
}

func (s *Server) runKnowledgeReview(jobID, submissionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.1, "Loading submission and review policy")
	claimed, err := s.Store.MarkSubmissionReviewing(ctx, submissionID)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if !claimed {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Submission was already reviewed")
		return
	}
	item, _, err := s.Store.GetKnowledgeSubmission(ctx, submissionID, true)
	if err != nil {
		_ = s.Store.SetSubmissionReviewError(ctx, submissionID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	library, ok, err := s.findLibrary(ctx, item.LibraryID)
	if err != nil || !ok || !library.AutoReviewAgentSubmissions || library.ReviewProviderID == "" {
		err = errors.New("automatic review is not configured for this library")
		_ = s.Store.SetSubmissionReviewError(ctx, submissionID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	provider, err := s.Store.GetProvider(ctx, library.ReviewProviderID)
	if err != nil {
		_ = s.Store.SetSubmissionReviewError(ctx, submissionID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	if !provider.Local {
		allowed, allowErr := s.Store.LibrariesAllowRemote(ctx, []string{library.ID})
		if allowErr != nil || !allowed {
			err = errors.New("remote review is not allowed for this library")
			_ = s.Store.SetSubmissionReviewError(ctx, submissionID, err)
			s.failJob(ctx, jobID, err)
			return
		}
	}
	evidence, err := s.Store.Search(ctx, model.QueryRequest{Query: item.Title + " " + item.Summary, LibraryIDs: []string{item.LibraryID}, TopK: 8})
	if err != nil {
		_ = s.Store.SetSubmissionReviewError(ctx, submissionID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	key, err := secrets.Get("provider:" + provider.ID)
	if err != nil {
		_ = s.Store.SetSubmissionReviewError(ctx, submissionID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.45, "Calling configured review model")
	raw, err := s.Providers.Review(ctx, provider, key, item.Markdown, evidence)
	if err != nil {
		_ = s.Store.SetSubmissionReviewError(ctx, submissionID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	var output reviewModelOutput
	decoder := json.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(&output); err != nil || (output.Decision != "approve" && output.Decision != "reject" && output.Decision != "needs_human") {
		if err == nil {
			err = errors.New("review model returned an invalid decision")
		}
		_ = s.Store.SetSubmissionReviewError(ctx, submissionID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	issues := make([]string, 0, len(output.Issues))
	for _, issue := range output.Issues {
		issues = append(issues, issue.Severity+":"+issue.Code+":"+issue.Message)
	}
	if output.Decision == "approve" && (output.Confidence < 0.85 || hasBlockingReviewIssue(output.Issues)) {
		output.Decision = "needs_human"
		output.Reason = "Model confidence or issue severity requires human review: " + output.Reason
	}
	changed, err := s.Store.RecordSubmissionReview(ctx, submissionID, "model", provider.ID, output.Decision, output.Confidence, output.Reason, issues)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if !changed {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Submission was reviewed by another actor")
		return
	}
	if output.Decision == "approve" {
		publish, publishErr := s.Store.CreateJob(ctx, "knowledge_publish", map[string]any{"submissionId": submissionID})
		if publishErr != nil {
			_ = s.Store.SetSubmissionPublishError(ctx, submissionID, publishErr)
			s.failJob(ctx, jobID, publishErr)
			return
		}
		go s.runKnowledgePublish(publish.ID, submissionID)
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Review decision: "+output.Decision)
}

func hasBlockingReviewIssue(issues []reviewModelIssue) bool {
	for _, issue := range issues {
		if strings.EqualFold(issue.Severity, "error") || strings.EqualFold(issue.Severity, "critical") {
			return true
		}
	}
	return false
}

func (s *Server) runKnowledgePublish(jobID, submissionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.2, "Publishing approved knowledge")
	item, _, err := s.Store.GetKnowledgeSubmission(ctx, submissionID, false)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if item.Status != "approved_pending_index" {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Submission is not waiting for index publication")
		return
	}
	detail, err := s.Store.GetDocument(ctx, item.DocumentID)
	if err != nil {
		_ = s.Store.SetSubmissionPublishError(ctx, submissionID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	if s.Worker == nil {
		err = errors.New("worker unavailable")
		_ = s.Store.SetSubmissionPublishError(ctx, submissionID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	if err := s.Worker.Call(ctx, "index_upsert", map[string]any{"libraryId": detail.LibraryID, "documentId": detail.ID, "chunks": detail.Preview}, nil); err != nil {
		_ = s.Store.SetSubmissionPublishError(ctx, submissionID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	published, err := s.Store.MarkSubmissionPublished(ctx, submissionID)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if !published {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Submission was published by another worker")
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Knowledge published to formal index")
}

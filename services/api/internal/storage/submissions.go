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
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/google/uuid"
)

var (
	ErrSubmissionTicketInvalid = errors.New("submission ticket is invalid")
	ErrSubmissionTicketExpired = errors.New("submission ticket has expired")
	ErrSubmissionTicketUsed    = errors.New("submission ticket has already been used")
	ErrSubmissionDuplicate     = errors.New("submission content already exists")
	ErrSubmissionConflict      = errors.New("submission revision is not eligible")
	ErrSubmissionNotFound      = errors.New("knowledge submission not found")
)

type SubmissionTicket struct {
	ID                 string
	TokenID            string
	LibraryID          string
	FormatterSkillID   string
	FormatterSkillHash string
	ExpiresAt          time.Time
}

type SubmissionCreateInput struct {
	TokenID                string
	LibraryID              string
	ClientSubmissionID     string
	Ticket                 string
	FormatterSkillID       string
	FormatterSkillHash     string
	SupersedesSubmissionID string
	Document               model.Document
	Chunks                 []model.Chunk
	Summary                string
	Tags                   []string
	Provenance             map[string]any
}

func submissionTicketHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CreateSubmissionTicket(ctx context.Context, tokenID, libraryID, formatterSkillID, formatterSkillHash string, expiresAt time.Time) (string, error) {
	secret := "kah_submit_" + uuid.NewString() + uuid.NewString()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO submission_tickets(id,ticket_hash,token_id,library_id,formatter_skill_id,formatter_skill_hash,expires_at) VALUES(?,?,?,?,?,?,?)`, uuid.NewString(), submissionTicketHash(secret), tokenID, libraryID, formatterSkillID, formatterSkillHash, expiresAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", err
	}
	return secret, nil
}

func loadSubmissionTicket(ctx context.Context, tx *sql.Tx, raw, tokenID, libraryID, skillID, skillHash string) (SubmissionTicket, error) {
	var ticket SubmissionTicket
	var expires string
	var consumed sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id,token_id,library_id,formatter_skill_id,formatter_skill_hash,expires_at,consumed_at FROM submission_tickets WHERE ticket_hash=?`, submissionTicketHash(raw)).Scan(&ticket.ID, &ticket.TokenID, &ticket.LibraryID, &ticket.FormatterSkillID, &ticket.FormatterSkillHash, &expires, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return ticket, ErrSubmissionTicketInvalid
	}
	if err != nil {
		return ticket, err
	}
	if ticket.TokenID != tokenID || ticket.LibraryID != libraryID || ticket.FormatterSkillID != skillID || ticket.FormatterSkillHash != skillHash {
		return ticket, ErrSubmissionTicketInvalid
	}
	ticket.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return ticket, ErrSubmissionTicketInvalid
	}
	if consumed.Valid {
		return ticket, ErrSubmissionTicketUsed
	}
	if !ticket.ExpiresAt.After(time.Now().UTC()) {
		return ticket, ErrSubmissionTicketExpired
	}
	return ticket, nil
}

func (s *Store) CreateKnowledgeSubmission(ctx context.Context, input SubmissionCreateInput) (model.KnowledgeSubmission, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.KnowledgeSubmission{}, false, err
	}
	defer tx.Rollback()

	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM knowledge_submissions WHERE submitted_by_token_id=? AND client_submission_id=?`, input.TokenID, input.ClientSubmissionID).Scan(&existingID)
	if err == nil {
		tx.Rollback()
		item, _, getErr := s.GetKnowledgeSubmission(ctx, existingID, false)
		return item, true, getErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.KnowledgeSubmission{}, false, err
	}
	if _, err := loadSubmissionTicket(ctx, tx, input.Ticket, input.TokenID, input.LibraryID, input.FormatterSkillID, input.FormatterSkillHash); err != nil {
		return model.KnowledgeSubmission{}, false, err
	}
	var duplicateID, duplicateStatus string
	err = tx.QueryRowContext(ctx, `SELECT id,status FROM documents WHERE library_id=? AND content_hash=? LIMIT 1`, input.LibraryID, input.Document.ContentHash).Scan(&duplicateID, &duplicateStatus)
	if err == nil {
		return model.KnowledgeSubmission{}, false, fmt.Errorf("%w: document %s is %s", ErrSubmissionDuplicate, duplicateID, duplicateStatus)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.KnowledgeSubmission{}, false, err
	}
	if input.SupersedesSubmissionID != "" {
		var revisionLibrary, revisionToken, revisionStatus string
		err := tx.QueryRowContext(ctx, `SELECT library_id,submitted_by_token_id,review_status FROM knowledge_submissions WHERE id=?`, input.SupersedesSubmissionID).Scan(&revisionLibrary, &revisionToken, &revisionStatus)
		if err != nil || revisionLibrary != input.LibraryID || revisionToken != input.TokenID || revisionStatus != "rejected" {
			return model.KnowledgeSubmission{}, false, ErrSubmissionConflict
		}
	}

	_, stamp := now()
	tagsJSON, _ := json.Marshal(input.Tags)
	provenanceJSON, _ := json.Marshal(input.Provenance)
	if _, err := tx.ExecContext(ctx, `INSERT INTO documents(id,library_id,title,media_type,source_path,source_url,object_path,content_hash,status,error,tags_json,favorite,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'',?,0,?,?)`, input.Document.ID, input.Document.LibraryID, input.Document.Title, input.Document.MediaType, input.Document.SourcePath, input.Document.SourceURL, input.Document.ObjectPath, input.Document.ContentHash, "pending_review", string(tagsJSON), stamp, stamp); err != nil {
		return model.KnowledgeSubmission{}, false, err
	}
	for index, chunk := range input.Chunks {
		location, _ := json.Marshal(chunk.Location)
		if _, err := tx.ExecContext(ctx, `INSERT INTO chunks(id,document_id,ordinal,text,location_json,content_hash) VALUES(?,?,?,?,?,?)`, chunk.ID, input.Document.ID, index, chunk.Text, string(location), chunk.ContentHash); err != nil {
			return model.KnowledgeSubmission{}, false, err
		}
	}
	submissionID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_submissions(id,document_id,library_id,submitted_by_token_id,client_submission_id,formatter_skill_id,formatter_skill_hash,supersedes_submission_id,review_status,summary,tags_json,provenance_json,submitted_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, submissionID, input.Document.ID, input.LibraryID, input.TokenID, input.ClientSubmissionID, input.FormatterSkillID, input.FormatterSkillHash, nullableString(input.SupersedesSubmissionID), "pending_review", input.Summary, string(tagsJSON), string(provenanceJSON), stamp, stamp); err != nil {
		return model.KnowledgeSubmission{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE submission_tickets SET consumed_at=? WHERE ticket_hash=? AND consumed_at IS NULL`, stamp, submissionTicketHash(input.Ticket)); err != nil {
		return model.KnowledgeSubmission{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return model.KnowledgeSubmission{}, false, err
	}
	item, _, err := s.GetKnowledgeSubmission(ctx, submissionID, false)
	return item, false, err
}

func (s *Store) KnowledgeSubmissionOwner(ctx context.Context, id string) (string, string, error) {
	var tokenID, libraryID string
	err := s.DB.QueryRowContext(ctx, `SELECT submitted_by_token_id,library_id FROM knowledge_submissions WHERE id=?`, id).Scan(&tokenID, &libraryID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrSubmissionNotFound
	}
	return tokenID, libraryID, err
}
func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

type submissionScan struct {
	ID, DocumentID, LibraryID, SubmittedByTokenID                  string
	ClientSubmissionID, FormatterSkillID, FormatterSkillHash       string
	SupersedesSubmissionID, ReviewStatus, ReviewJobID, ReviewError string
	Summary, TagsJSON, ProvenanceJSON, SubmittedAt, UpdatedAt      string
	Document                                                       model.Document
}

func scanSubmission(row interface{ Scan(...any) error }) (submissionScan, error) {
	var item submissionScan
	var supersedes sql.NullString
	var tags, provenance, created, updated string
	var sourcePath, sourceURL, objectPath, contentHash, docError string
	var favorite int
	err := row.Scan(&item.ID, &item.DocumentID, &item.LibraryID, &item.SubmittedByTokenID, &item.ClientSubmissionID, &item.FormatterSkillID, &item.FormatterSkillHash, &supersedes, &item.ReviewStatus, &item.ReviewJobID, &item.ReviewError, &item.Summary, &tags, &provenance, &item.SubmittedAt, &item.UpdatedAt, &item.Document.Title, &item.Document.MediaType, &sourcePath, &sourceURL, &objectPath, &contentHash, &item.Document.Status, &docError, &favorite, &created, &updated)
	if err != nil {
		return item, err
	}
	item.SupersedesSubmissionID = supersedes.String
	item.TagsJSON, item.ProvenanceJSON = tags, provenance
	item.Document.ID, item.Document.LibraryID = item.DocumentID, item.LibraryID
	item.Document.SourcePath, item.Document.SourceURL, item.Document.ObjectPath, item.Document.ContentHash = sourcePath, sourceURL, objectPath, contentHash
	item.Document.Error, item.Document.Favorite = docError, favorite == 1
	_ = json.Unmarshal([]byte(tags), &item.Document.Tags)
	if item.Document.Tags == nil {
		item.Document.Tags = []string{}
	}
	item.Document.CreatedAt, item.Document.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

const submissionSelect = `SELECT s.id,s.document_id,s.library_id,s.submitted_by_token_id,s.client_submission_id,s.formatter_skill_id,s.formatter_skill_hash,s.supersedes_submission_id,s.review_status,s.review_job_id,s.review_error,s.summary,s.tags_json,s.provenance_json,s.submitted_at,s.updated_at,d.title,d.media_type,d.source_path,d.source_url,d.object_path,d.content_hash,d.status,d.error,d.favorite,d.created_at,d.updated_at FROM knowledge_submissions s JOIN documents d ON d.id=s.document_id`

func (s *Store) GetKnowledgeSubmission(ctx context.Context, id string, includeMarkdown bool) (model.KnowledgeSubmission, bool, error) {
	item, err := scanSubmission(s.DB.QueryRowContext(ctx, submissionSelect+` WHERE s.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.KnowledgeSubmission{}, false, ErrSubmissionNotFound
	}
	if err != nil {
		return model.KnowledgeSubmission{}, false, err
	}
	result := submissionModel(item)
	result.Document = &item.Document
	result.Reviews, err = s.listReviewRecords(ctx, item.ID)
	if err != nil {
		return model.KnowledgeSubmission{}, false, err
	}
	if includeMarkdown {
		content, err := s.readObject(item.Document.ObjectPath)
		if err != nil {
			return model.KnowledgeSubmission{}, false, err
		}
		result.Markdown = string(content)
	}
	return result, false, nil
}

func submissionModel(item submissionScan) model.KnowledgeSubmission {
	var tags []string
	var provenance map[string]any
	_ = json.Unmarshal([]byte(item.TagsJSON), &tags)
	_ = json.Unmarshal([]byte(item.ProvenanceJSON), &provenance)
	if tags == nil {
		tags = []string{}
	}
	if provenance == nil {
		provenance = map[string]any{}
	}
	return model.KnowledgeSubmission{ID: item.ID, DocumentID: item.DocumentID, LibraryID: item.LibraryID, Title: item.Document.Title, Summary: item.Summary, Tags: tags, Provenance: provenance, ContentHash: item.Document.ContentHash, Status: item.Document.Status, ReviewStatus: item.ReviewStatus, FormatterSkillID: item.FormatterSkillID, FormatterSkillHash: item.FormatterSkillHash, SupersedesSubmissionID: item.SupersedesSubmissionID, ReviewJobID: item.ReviewJobID, ReviewError: item.ReviewError, SubmittedAt: parseTime(item.SubmittedAt), UpdatedAt: parseTime(item.UpdatedAt)}
}

func (s *Store) ListKnowledgeSubmissions(ctx context.Context, tokenID, libraryID, status string, ownOnly bool) ([]model.KnowledgeSubmission, error) {
	query := submissionSelect + ` WHERE 1=1`
	args := []any{}
	if libraryID != "" {
		query += ` AND s.library_id=?`
		args = append(args, libraryID)
	}
	if status != "" {
		query += ` AND s.review_status=?`
		args = append(args, status)
	}
	if ownOnly {
		query += ` AND s.submitted_by_token_id=?`
		args = append(args, tokenID)
	}
	query += ` ORDER BY s.updated_at DESC LIMIT 200`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	scans := []submissionScan{}
	for rows.Next() {
		row, scanErr := scanSubmission(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		scans = append(scans, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	items := make([]model.KnowledgeSubmission, 0, len(scans))
	for _, row := range scans {
		item := submissionModel(row)
		item.Document = &row.Document
		item.Reviews, err = s.listReviewRecords(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) readObject(relative string) ([]byte, error) {
	path, err := s.Resolve(relative)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (s *Store) MarkSubmissionReviewing(ctx context.Context, id string) (bool, error) {
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, `UPDATE knowledge_submissions SET review_status='reviewing',review_error='',updated_at=? WHERE id=? AND review_status IN ('pending_review','reviewing')`, stamp, id)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

func (s *Store) SetSubmissionReviewJob(ctx context.Context, id, jobID string) error {
	_, stamp := now()
	_, err := s.DB.ExecContext(ctx, `UPDATE knowledge_submissions SET review_job_id=?,updated_at=? WHERE id=?`, jobID, stamp, id)
	return err
}

func (s *Store) RecordSubmissionReview(ctx context.Context, id, reviewerType, reviewer, decision string, confidence float64, reason string, issues []string) (bool, error) {
	if reviewerType != "model" && reviewerType != "human" {
		return false, errors.New("invalid reviewer type")
	}
	if decision != "approve" && decision != "reject" && decision != "needs_human" {
		return false, errors.New("invalid review decision")
	}
	targetStatus := "pending_review"
	documentStatus := "pending_review"
	if decision == "approve" {
		targetStatus, documentStatus = "approved_pending_index", "approved_pending_index"
	} else if decision == "reject" {
		targetStatus, documentStatus = "rejected", "rejected"
	}
	issuesJSON, _ := json.Marshal(issues)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	_, stamp := now()
	statusClause := `('pending_review','reviewing')`
	if reviewerType == "human" && (decision == "approve" || decision == "reject") {
		var latestReviewerType, latestDecision string
		latestErr := tx.QueryRowContext(ctx, `SELECT reviewer_type,decision FROM knowledge_reviews WHERE submission_id=? ORDER BY created_at DESC LIMIT 1`, id).Scan(&latestReviewerType, &latestDecision)
		if latestErr != nil && !errors.Is(latestErr, sql.ErrNoRows) {
			return false, latestErr
		}
		if latestReviewerType == "model" && latestDecision == "reject" {
			statusClause = `('pending_review','reviewing','rejected')`
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_submissions SET review_status=?,review_error='',updated_at=? WHERE id=? AND review_status IN `+statusClause, targetStatus, stamp, id)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE documents SET status=?,error='',updated_at=? WHERE id=(SELECT document_id FROM knowledge_submissions WHERE id=?)`, documentStatus, stamp, id); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_reviews(id,submission_id,reviewer_type,reviewer,decision,confidence,reason,issues_json,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), id, reviewerType, reviewer, decision, confidence, reason, string(issuesJSON), stamp); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) SetSubmissionReviewError(ctx context.Context, id string, cause error) error {
	_, stamp := now()
	_, err := s.DB.ExecContext(ctx, `UPDATE knowledge_submissions SET review_status='pending_review',review_error=?,updated_at=? WHERE id=? AND review_status IN ('pending_review','reviewing')`, cause.Error(), stamp, id)
	return err
}

func (s *Store) MarkSubmissionPublished(ctx context.Context, id string) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	_, stamp := now()
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_submissions SET review_status='published',review_error='',updated_at=? WHERE id=? AND review_status='approved_pending_index'`, stamp, id)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE documents SET status='ready',error='',updated_at=? WHERE id=(SELECT document_id FROM knowledge_submissions WHERE id=?)`, stamp, id); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) SetSubmissionPublishError(ctx context.Context, id string, cause error) error {
	_, stamp := now()
	_, err := s.DB.ExecContext(ctx, `UPDATE knowledge_submissions SET review_error=?,updated_at=? WHERE id=? AND review_status='approved_pending_index'`, cause.Error(), stamp, id)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE documents SET error=?,updated_at=? WHERE id=(SELECT document_id FROM knowledge_submissions WHERE id=?) AND status='approved_pending_index'`, cause.Error(), stamp, id)
	return err
}

func (s *Store) ResetSubmissionReview(ctx context.Context, id string) error {
	_, stamp := now()
	_, err := s.DB.ExecContext(ctx, `UPDATE knowledge_submissions SET review_status='pending_review',review_error='',updated_at=? WHERE id=? AND review_status IN ('pending_review','reviewing')`, stamp, id)
	return err
}

func (s *Store) listReviewRecords(ctx context.Context, submissionID string) ([]model.ReviewRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,reviewer_type,reviewer,decision,confidence,reason,issues_json,created_at FROM knowledge_reviews WHERE submission_id=? ORDER BY created_at`, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.ReviewRecord{}
	for rows.Next() {
		var item model.ReviewRecord
		var issues, created string
		if err := rows.Scan(&item.ID, &item.ReviewerType, &item.Reviewer, &item.Decision, &item.Confidence, &item.Reason, &issues, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(issues), &item.Issues)
		if item.Issues == nil {
			item.Issues = []string{}
		}
		item.CreatedAt = parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}

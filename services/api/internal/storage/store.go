package storage

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB       *sql.DB
	DataRoot string
}

// QueuedJob is the internal form used by the startup recovery loop. The
// payload is deliberately not part of model.Job so that file paths and other
// replay details are never returned by the public jobs endpoint.
type QueuedJob struct {
	model.Job
	Payload      map[string]any
	PayloadError error
}

func Open(dataRoot string) (*Store, error) {
	for _, name := range []string{"objects", "indexes", "logs", "backups", "staging", "skills"} {
		if err := os.MkdirAll(filepath.Join(dataRoot, name), 0o750); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "knowledge.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{DB: db, DataRoot: dataRoot}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate() error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS libraries (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
			allow_remote_models INTEGER NOT NULL DEFAULT 0, auto_review_agent_submissions INTEGER NOT NULL DEFAULT 0,
			review_provider_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS documents (
			id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			title TEXT NOT NULL, media_type TEXT NOT NULL, source_path TEXT NOT NULL DEFAULT '',
			source_url TEXT NOT NULL DEFAULT '', object_path TEXT NOT NULL DEFAULT '', content_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', tags_json TEXT NOT NULL DEFAULT '[]', favorite INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_library ON documents(library_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id TEXT PRIMARY KEY, document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
			ordinal INTEGER NOT NULL, text TEXT NOT NULL, location_json TEXT NOT NULL, content_hash TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_document ON chunks(document_id, ordinal)`,
		`CREATE TABLE IF NOT EXISTS skills (
			id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL,
			compatibility TEXT NOT NULL DEFAULT '', license TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}',
			allowed_tools TEXT NOT NULL DEFAULT '', root_path TEXT NOT NULL, entry_point TEXT NOT NULL DEFAULT 'SKILL.md',
			content_hash TEXT NOT NULL, status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skills_name ON skills(lower(name))`,
		`CREATE TABLE IF NOT EXISTS skill_files (
			skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
			path TEXT NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL, media_type TEXT NOT NULL,
			PRIMARY KEY(skill_id, path)
		)`,
		`CREATE TABLE IF NOT EXISTS skill_library_links (
			skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
			library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			relation TEXT NOT NULL CHECK(relation IN ('skill_uses_library','library_requires_skill')),
			PRIMARY KEY(skill_id, library_id, relation)
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, status TEXT NOT NULL, progress REAL NOT NULL DEFAULT 0,
			message TEXT NOT NULL DEFAULT '', payload_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agent_tokens (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, scopes_json TEXT NOT NULL,
			library_ids_json TEXT NOT NULL DEFAULT '[]', created_at TEXT NOT NULL, revoked_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS providers (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, base_url TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT '', embedding_model TEXT NOT NULL DEFAULT '', local INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS saved_searches (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, query TEXT NOT NULL, library_ids_json TEXT NOT NULL DEFAULT '[]', tags_json TEXT NOT NULL DEFAULT '[]'
		)`,
		`CREATE TABLE IF NOT EXISTS virtual_folders (
			id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			name TEXT NOT NULL, parent_id TEXT REFERENCES virtual_folders(id) ON DELETE CASCADE,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(library_id, parent_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS document_folders (
			document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
			folder_id TEXT NOT NULL REFERENCES virtual_folders(id) ON DELETE CASCADE,
			PRIMARY KEY(document_id, folder_id)
		)`,
		`CREATE TABLE IF NOT EXISTS source_watches (
			id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			root_path TEXT NOT NULL, recursive INTEGER NOT NULL DEFAULT 1, enabled INTEGER NOT NULL DEFAULT 1,
			last_scan_at TEXT, last_message TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS feedback (
			id TEXT PRIMARY KEY, request_id TEXT NOT NULL, chunk_id TEXT NOT NULL, relevant INTEGER NOT NULL,
			note TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS query_evidence (
			request_id TEXT NOT NULL, chunk_id TEXT NOT NULL, created_at TEXT NOT NULL,
			PRIMARY KEY(request_id, chunk_id)
		)`,
		`CREATE TABLE IF NOT EXISTS system_skills (
			role TEXT PRIMARY KEY, skill_id TEXT NOT NULL UNIQUE REFERENCES skills(id) ON DELETE CASCADE,
			content_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS knowledge_submissions (
			id TEXT PRIMARY KEY, document_id TEXT NOT NULL UNIQUE REFERENCES documents(id) ON DELETE CASCADE,
			library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			submitted_by_token_id TEXT NOT NULL REFERENCES agent_tokens(id),
			client_submission_id TEXT NOT NULL, formatter_skill_id TEXT NOT NULL,
			formatter_skill_hash TEXT NOT NULL, supersedes_submission_id TEXT REFERENCES knowledge_submissions(id),
			review_status TEXT NOT NULL, review_job_id TEXT NOT NULL DEFAULT '',
			review_error TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '',
			tags_json TEXT NOT NULL DEFAULT '[]', provenance_json TEXT NOT NULL DEFAULT '{}',
			submitted_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			UNIQUE(submitted_by_token_id, client_submission_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_submissions_library_status ON knowledge_submissions(library_id, review_status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS submission_tickets (
			id TEXT PRIMARY KEY, ticket_hash TEXT NOT NULL UNIQUE,
			token_id TEXT NOT NULL REFERENCES agent_tokens(id) ON DELETE CASCADE,
			library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			formatter_skill_id TEXT NOT NULL, formatter_skill_hash TEXT NOT NULL,
			expires_at TEXT NOT NULL, consumed_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_submission_tickets_expiry ON submission_tickets(expires_at)`,
		`CREATE TABLE IF NOT EXISTS knowledge_reviews (
			id TEXT PRIMARY KEY, submission_id TEXT NOT NULL REFERENCES knowledge_submissions(id) ON DELETE CASCADE,
			reviewer_type TEXT NOT NULL, reviewer TEXT NOT NULL, decision TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0, reason TEXT NOT NULL DEFAULT '',
			issues_json TEXT NOT NULL DEFAULT '[]', created_at TEXT NOT NULL
		)`, `CREATE TABLE IF NOT EXISTS audit_log (
			id TEXT PRIMARY KEY, actor TEXT NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL DEFAULT '',
			details_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL
		)`,
		`UPDATE jobs SET status='queued', message='Recovered after restart' WHERE status='running'`,
	}
	for _, statement := range statements {
		if _, err := s.DB.Exec(statement); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	var favoriteColumn string
	err := s.DB.QueryRow(`SELECT name FROM pragma_table_info('documents') WHERE name='favorite'`).Scan(&favoriteColumn)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.DB.Exec(`ALTER TABLE documents ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migration failed adding favorite: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("migration failed checking favorite: %w", err)
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"auto_review_agent_submissions", "INTEGER NOT NULL DEFAULT 0"},
		{"review_provider_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		var existing string
		err := s.DB.QueryRow(`SELECT name FROM pragma_table_info('libraries') WHERE name=?`, column.name).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := s.DB.Exec(`ALTER TABLE libraries ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
				return fmt.Errorf("migration failed adding libraries.%s: %w", column.name, err)
			}
		} else if err != nil {
			return fmt.Errorf("migration failed checking libraries.%s: %w", column.name, err)
		}
	}
	return nil
}

func now() (time.Time, string) {
	t := time.Now().UTC()
	return t, t.Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) ListLibraries(ctx context.Context) ([]model.Library, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,description,allow_remote_models,auto_review_agent_submissions,review_provider_id,created_at,updated_at FROM libraries ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Library{}
	for rows.Next() {
		var item model.Library
		var allow, autoReview int
		var created, updated string
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &allow, &autoReview, &item.ReviewProviderID, &created, &updated); err != nil {
			return nil, err
		}
		item.AllowRemoteModels = allow == 1
		item.AutoReviewAgentSubmissions = autoReview == 1
		item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateLibrary(ctx context.Context, name, description string) (model.Library, error) {
	if strings.TrimSpace(name) == "" {
		return model.Library{}, errors.New("name is required")
	}
	t, stamp := now()
	item := model.Library{ID: uuid.NewString(), Name: strings.TrimSpace(name), Description: strings.TrimSpace(description), CreatedAt: t, UpdatedAt: t}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO libraries(id,name,description,allow_remote_models,auto_review_agent_submissions,review_provider_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, item.ID, item.Name, item.Description, 0, 0, "", stamp, stamp)
	return item, err
}

func (s *Store) UpdateLibrary(ctx context.Context, id string, name, description *string, allow, autoReview *bool, reviewProviderID *string) (model.Library, error) {
	var current model.Library
	var allowInt, autoReviewInt int
	var created, updated string
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,description,allow_remote_models,auto_review_agent_submissions,review_provider_id,created_at,updated_at FROM libraries WHERE id=?`, id).Scan(&current.ID, &current.Name, &current.Description, &allowInt, &autoReviewInt, &current.ReviewProviderID, &created, &updated)
	if err != nil {
		return current, err
	}
	current.AllowRemoteModels = allowInt == 1
	current.AutoReviewAgentSubmissions = autoReviewInt == 1
	current.CreatedAt = parseTime(created)
	if name != nil && strings.TrimSpace(*name) != "" {
		current.Name = strings.TrimSpace(*name)
	}
	if description != nil {
		current.Description = strings.TrimSpace(*description)
	}
	if allow != nil {
		current.AllowRemoteModels = *allow
	}
	if autoReview != nil {
		current.AutoReviewAgentSubmissions = *autoReview
	}
	if reviewProviderID != nil {
		current.ReviewProviderID = strings.TrimSpace(*reviewProviderID)
	}
	current.UpdatedAt, updated = now()
	_, err = s.DB.ExecContext(ctx, `UPDATE libraries SET name=?,description=?,allow_remote_models=?,auto_review_agent_submissions=?,review_provider_id=?,updated_at=? WHERE id=?`, current.Name, current.Description, boolInt(current.AllowRemoteModels), boolInt(current.AutoReviewAgentSubmissions), current.ReviewProviderID, updated, id)
	return current, err
}

func (s *Store) DeleteLibrary(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM libraries WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ListDocuments(ctx context.Context, libraryID string) ([]model.Document, error) {
	query := `SELECT id,library_id,title,media_type,source_path,source_url,object_path,content_hash,status,error,tags_json,favorite,created_at,updated_at FROM documents`
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
	defer rows.Close()
	items := []model.Document{}
	for rows.Next() {
		item, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanDocument(row rowScanner) (model.Document, error) {
	var item model.Document
	var tags, created, updated string
	var favorite int
	err := row.Scan(&item.ID, &item.LibraryID, &item.Title, &item.MediaType, &item.SourcePath, &item.SourceURL, &item.ObjectPath, &item.ContentHash, &item.Status, &item.Error, &tags, &favorite, &created, &updated)
	if err != nil {
		return item, err
	}
	_ = json.Unmarshal([]byte(tags), &item.Tags)
	if item.Tags == nil {
		item.Tags = []string{}
	}
	item.Favorite = favorite == 1
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

func (s *Store) GetDocument(ctx context.Context, id string) (model.DocumentDetail, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id,library_id,title,media_type,source_path,source_url,object_path,content_hash,status,error,tags_json,favorite,created_at,updated_at FROM documents WHERE id=?`, id)
	doc, err := scanDocument(row)
	if err != nil {
		return model.DocumentDetail{}, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,document_id,text,location_json,content_hash FROM chunks WHERE document_id=? ORDER BY ordinal LIMIT 500`, id)
	if err != nil {
		return model.DocumentDetail{}, err
	}
	defer rows.Close()
	chunks := []model.Chunk{}
	for rows.Next() {
		var chunk model.Chunk
		var location string
		if err := rows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.Text, &location, &chunk.ContentHash); err != nil {
			return model.DocumentDetail{}, err
		}
		_ = json.Unmarshal([]byte(location), &chunk.Location)
		chunks = append(chunks, chunk)
	}
	return model.DocumentDetail{Document: doc, Preview: chunks}, rows.Err()
}

func (s *Store) DocumentNeedsTextRepair(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM chunks WHERE document_id=? AND instr(text, ?) > 0`, id, "�").Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) CreatePendingDocument(ctx context.Context, libraryID, title, mediaType, sourcePath, sourceURL, objectPath, hash string) (model.Document, error) {
	t, stamp := now()
	doc := model.Document{ID: uuid.NewString(), LibraryID: libraryID, Title: title, MediaType: mediaType, SourcePath: sourcePath, SourceURL: sourceURL, ObjectPath: objectPath, ContentHash: hash, Status: "pending", Tags: []string{}, CreatedAt: t, UpdatedAt: t}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO documents(id,library_id,title,media_type,source_path,source_url,object_path,content_hash,status,error,tags_json,favorite,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'','[]',0,?,?)`, doc.ID, doc.LibraryID, doc.Title, doc.MediaType, doc.SourcePath, doc.SourceURL, doc.ObjectPath, doc.ContentHash, doc.Status, stamp, stamp)
	return doc, err
}

func (s *Store) ReplaceChunks(ctx context.Context, documentID string, chunks []model.Chunk) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE document_id=?`, documentID); err != nil {
		return err
	}
	for i, chunk := range chunks {
		location, _ := json.Marshal(chunk.Location)
		if _, err := tx.ExecContext(ctx, `INSERT INTO chunks(id,document_id,ordinal,text,location_json,content_hash) VALUES(?,?,?,?,?,?)`, chunk.ID, documentID, i, chunk.Text, string(location), chunk.ContentHash); err != nil {
			return err
		}
	}
	_, stamp := now()
	if _, err := tx.ExecContext(ctx, `UPDATE documents SET status='ready',error='',updated_at=? WHERE id=?`, stamp, documentID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateDocumentContent(ctx context.Context, id, objectPath, contentHash string) error {
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, `UPDATE documents SET object_path=?,content_hash=?,status='pending',error='',updated_at=? WHERE id=?`, objectPath, contentHash, stamp, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) FailDocument(ctx context.Context, id string, cause error) error {
	_, stamp := now()
	_, err := s.DB.ExecContext(ctx, `UPDATE documents SET status='failed',error=?,updated_at=? WHERE id=?`, cause.Error(), stamp, id)
	return err
}

func (s *Store) UpdateDocument(ctx context.Context, id string, title *string, tags []string, favorite *bool) (model.Document, error) {
	detail, err := s.GetDocument(ctx, id)
	if err != nil {
		return model.Document{}, err
	}
	if title != nil && strings.TrimSpace(*title) != "" {
		detail.Title = strings.TrimSpace(*title)
	}
	if tags != nil {
		detail.Tags = tags
	}
	if favorite != nil {
		detail.Favorite = *favorite
	}
	tagsJSON, _ := json.Marshal(detail.Tags)
	_, stamp := now()
	_, err = s.DB.ExecContext(ctx, `UPDATE documents SET title=?,tags_json=?,favorite=?,updated_at=? WHERE id=?`, detail.Title, string(tagsJSON), boolInt(detail.Favorite), stamp, id)
	if err != nil {
		return model.Document{}, err
	}
	detail.UpdatedAt = parseTime(stamp)
	return detail.Document, nil
}

func (s *Store) DeleteDocument(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM documents WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

var (
	ErrSkillConflict = errors.New("skill name already exists")
	ErrSkillInvalid  = errors.New("invalid skill package")
)

type skillFrontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
	AllowedTools  string            `yaml:"allowed-tools"`
}

type skillFileRecord struct {
	Path      string
	Size      int64
	SHA256    string
	MediaType string
}

const (
	maxSkillMarkdownBytes int64 = 10 * 1024 * 1024
	maxSkillZipBytes      int64 = 100 * 1024 * 1024
	maxSkillExpandedBytes int64 = 500 * 1024 * 1024
	maxSkillFiles               = 10000
)

func parseSkillFrontmatter(content []byte) (skillFrontmatter, error) {
	text := strings.TrimPrefix(string(content), "\uFEFF")
	if !strings.HasPrefix(text, "---") {
		return skillFrontmatter{}, fmt.Errorf("%w: SKILL.md must start with YAML frontmatter", ErrSkillInvalid)
	}
	lineEnd := strings.IndexByte(text, '\n')
	if lineEnd < 0 {
		return skillFrontmatter{}, fmt.Errorf("%w: SKILL.md frontmatter is incomplete", ErrSkillInvalid)
	}
	rest := text[lineEnd+1:]
	closing := strings.Index(rest, "\n---")
	if closing < 0 {
		return skillFrontmatter{}, fmt.Errorf("%w: SKILL.md frontmatter is not closed", ErrSkillInvalid)
	}
	var frontmatter skillFrontmatter
	if err := yaml.Unmarshal([]byte(rest[:closing]), &frontmatter); err != nil {
		return skillFrontmatter{}, fmt.Errorf("%w: invalid YAML frontmatter: %v", ErrSkillInvalid, err)
	}
	frontmatter.Name = strings.TrimSpace(frontmatter.Name)
	frontmatter.Description = strings.TrimSpace(frontmatter.Description)
	if frontmatter.Metadata == nil {
		frontmatter.Metadata = map[string]string{}
	}
	if !validSkillName(frontmatter.Name) {
		return skillFrontmatter{}, fmt.Errorf("%w: name must use lowercase letters, numbers, and single hyphens", ErrSkillInvalid)
	}
	if frontmatter.Description == "" || len([]rune(frontmatter.Description)) > 1024 {
		return skillFrontmatter{}, fmt.Errorf("%w: description must contain 1-1024 characters", ErrSkillInvalid)
	}
	if len([]rune(frontmatter.Compatibility)) > 500 {
		return skillFrontmatter{}, fmt.Errorf("%w: compatibility must contain at most 500 characters", ErrSkillInvalid)
	}
	return frontmatter, nil
}

func validSkillName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return len([]rune(name)) <= 64
}

func normalizeSkillZipPath(value string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(value, "/") || (len(value) >= 2 && value[1] == ':') {
		return "", fmt.Errorf("%w: absolute archive path is forbidden", ErrSkillInvalid)
	}
	if value == "" {
		return "", fmt.Errorf("%w: empty archive path", ErrSkillInvalid)
	}
	parts := strings.Split(value, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." || strings.Contains(part, ":") {
			return "", fmt.Errorf("%w: archive path escapes package root", ErrSkillInvalid)
		}
		clean = append(clean, part)
	}
	if len(clean) == 0 {
		return "", fmt.Errorf("%w: empty archive path", ErrSkillInvalid)
	}
	return strings.Join(clean, "/"), nil
}

func isIgnorableSkillArchivePath(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) > 0 && strings.EqualFold(parts[0], "__MACOSX") {
		return true
	}
	base := strings.ToLower(filepath.Base(value))
	return base == ".ds_store" || base == "thumbs.db"
}
func copySkillSource(source, stage string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: source must be a regular file", ErrSkillInvalid)
	}
	extension := strings.ToLower(filepath.Ext(source))
	if extension == ".md" {
		if info.Size() > maxSkillMarkdownBytes {
			return fmt.Errorf("%w: SKILL.md exceeds the size limit", ErrSkillInvalid)
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.Create(filepath.Join(stage, "SKILL.md"))
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(input, maxSkillMarkdownBytes+1))
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written, _ := os.Stat(filepath.Join(stage, "SKILL.md")); written != nil && written.Size() > maxSkillMarkdownBytes {
			return fmt.Errorf("%w: SKILL.md exceeds the size limit", ErrSkillInvalid)
		}
		return nil
	}
	if extension != ".zip" {
		return fmt.Errorf("%w: only .md and .zip are supported", ErrSkillInvalid)
	}
	if info.Size() > maxSkillZipBytes {
		return fmt.Errorf("%w: Skill zip exceeds the size limit", ErrSkillInvalid)
	}
	archive, err := zip.OpenReader(source)
	if err != nil {
		return fmt.Errorf("%w: cannot open zip: %v", ErrSkillInvalid, err)
	}
	defer archive.Close()
	if len(archive.File) > maxSkillFiles {
		return fmt.Errorf("%w: Skill zip contains too many files", ErrSkillInvalid)
	}
	type archiveFile struct {
		source *zip.File
		path   string
	}
	files := []archiveFile{}
	seen := map[string]bool{}
	var expanded int64
	for _, entry := range archive.File {
		path, err := normalizeSkillZipPath(entry.Name)
		if err != nil {
			return err
		}
		if isIgnorableSkillArchivePath(path) {
			continue
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic links are not allowed", ErrSkillInvalid)
		}
		if seen[path] {
			return fmt.Errorf("%w: duplicate archive path %s", ErrSkillInvalid, path)
		}
		seen[path] = true
		expanded += int64(entry.UncompressedSize64)
		if expanded > maxSkillExpandedBytes {
			return fmt.Errorf("%w: expanded Skill exceeds the size limit", ErrSkillInvalid)
		}
		files = append(files, archiveFile{source: entry, path: path})
	}
	entryPoints := []string{}
	for _, file := range files {
		if strings.EqualFold(filepath.Base(file.path), "SKILL.md") {
			entryPoints = append(entryPoints, file.path)
		}
	}
	if len(entryPoints) != 1 {
		return fmt.Errorf("%w: zip must contain exactly one SKILL.md", ErrSkillInvalid)
	}
	entryDir := filepath.ToSlash(filepath.Dir(entryPoints[0]))
	if entryDir == "." {
		entryDir = ""
	}
	for _, file := range files {
		relative := file.path
		if entryDir != "" {
			prefix := entryDir + "/"
			if !strings.HasPrefix(relative, prefix) {
				return fmt.Errorf("%w: files must be inside the Skill root", ErrSkillInvalid)
			}
			relative = strings.TrimPrefix(relative, prefix)
		}
		if strings.EqualFold(relative, "SKILL.md") {
			relative = "SKILL.md"
		}
		target := filepath.Join(stage, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		input, err := file.source.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o640)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(input, maxSkillExpandedBytes+1))
		input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func collectSkillFiles(root string) ([]skillFileRecord, string, error) {
	files := []skillFileRecord{}
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%w: Skill contains a non-regular file", ErrSkillInvalid)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		handle, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, handle)
		closeErr := handle.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		files = append(files, skillFileRecord{Path: relative, Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil)), MediaType: skillMediaType(relative)})
		total += info.Size()
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	aggregate := sha256.New()
	for _, file := range files {
		_, _ = fmt.Fprintf(aggregate, "%s:%d:%s\n", file.Path, file.Size, file.SHA256)
	}
	_ = total
	return files, hex.EncodeToString(aggregate.Sum(nil)), nil
}

func skillMediaType(path string) string {
	if strings.EqualFold(filepath.Base(path), "SKILL.md") || strings.HasSuffix(strings.ToLower(path), ".md") {
		return "text/markdown"
	}
	if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); value != "" {
		return value
	}
	return "application/octet-stream"
}

func (s *Store) ImportSkill(ctx context.Context, source string, replace bool) (model.Skill, error) {
	source = filepath.Clean(source)
	if filepath.IsAbs(source) == false {
		source, _ = filepath.Abs(source)
	}
	stage, err := os.MkdirTemp(filepath.Join(s.DataRoot, "staging"), "skill-")
	if err != nil {
		return model.Skill{}, err
	}
	defer os.RemoveAll(stage)
	if err := copySkillSource(source, stage); err != nil {
		return model.Skill{}, err
	}
	entryPath := filepath.Join(stage, "SKILL.md")
	content, err := os.ReadFile(entryPath)
	if err != nil {
		return model.Skill{}, fmt.Errorf("%w: SKILL.md is required", ErrSkillInvalid)
	}
	frontmatter, err := parseSkillFrontmatter(content)
	if err != nil {
		return model.Skill{}, err
	}
	files, contentHash, err := collectSkillFiles(stage)
	if err != nil {
		return model.Skill{}, err
	}
	targetRelative := filepath.ToSlash(filepath.Join("skills", frontmatter.Name))
	target, err := s.Resolve(targetRelative)
	if err != nil {
		return model.Skill{}, err
	}
	var existingID string
	var existingRoot string
	err = s.DB.QueryRowContext(ctx, `SELECT id,root_path FROM skills WHERE name=?`, frontmatter.Name).Scan(&existingID, &existingRoot)
	if err == nil && !replace {
		return model.Skill{}, ErrSkillConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.Skill{}, err
	}
	if existingID != "" {
		systemSkill, systemErr := s.IsSystemSkill(ctx, existingID)
		if systemErr != nil {
			return model.Skill{}, systemErr
		}
		if systemSkill {
			return model.Skill{}, ErrSystemSkillProtected
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return model.Skill{}, err
	}
	backup := ""
	if existingID != "" {
		backup = target + ".old-" + uuid.NewString()
		if err := os.Rename(target, backup); err != nil {
			return model.Skill{}, err
		}
	}
	if err := os.Rename(stage, target); err != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return model.Skill{}, err
	}
	restore := func() {
		_ = os.RemoveAll(target)
		if backup != "" {
			_ = os.Rename(backup, target)
		}
	}
	t, stamp := now()
	item := model.Skill{ID: existingID, Name: frontmatter.Name, Description: frontmatter.Description, Compatibility: frontmatter.Compatibility, License: frontmatter.License, Metadata: frontmatter.Metadata, AllowedTools: frontmatter.AllowedTools, RootPath: targetRelative, EntryPoint: "SKILL.md", ContentHash: contentHash, Status: "ready", FileCount: len(files), CreatedAt: t, UpdatedAt: t}
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		restore()
		return model.Skill{}, err
	}
	defer tx.Rollback()
	if existingID == "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO skills(id,name,description,compatibility,license,metadata_json,allowed_tools,root_path,entry_point,content_hash,status,error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,'',?,?)`, item.ID, item.Name, item.Description, item.Compatibility, item.License, mustJSON(item.Metadata), item.AllowedTools, item.RootPath, item.EntryPoint, item.ContentHash, item.Status, stamp, stamp)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE skills SET description=?,compatibility=?,license=?,metadata_json=?,allowed_tools=?,root_path=?,entry_point=?,content_hash=?,status=?,error='',updated_at=? WHERE id=?`, item.Description, item.Compatibility, item.License, mustJSON(item.Metadata), item.AllowedTools, item.RootPath, item.EntryPoint, item.ContentHash, item.Status, stamp, item.ID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM skill_files WHERE skill_id=?`, item.ID)
		}
	}
	if err == nil {
		for _, file := range files {
			_, err = tx.ExecContext(ctx, `INSERT INTO skill_files(skill_id,path,size,sha256,media_type) VALUES(?,?,?,?,?)`, item.ID, file.Path, file.Size, file.SHA256, file.MediaType)
			if err != nil {
				break
			}
		}
	}
	if err != nil || tx.Commit() != nil {
		restore()
		if err == nil {
			err = errors.New("skill metadata commit failed")
		}
		return model.Skill{}, err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return s.GetSkill(ctx, item.ID)
}

func mustJSON(value any) string {
	bytes, _ := json.Marshal(value)
	return string(bytes)
}

func scanSkill(row rowScanner) (model.Skill, error) {
	var item model.Skill
	var metadata, created, updated string
	err := row.Scan(&item.ID, &item.Name, &item.Description, &item.Compatibility, &item.License, &metadata, &item.AllowedTools, &item.RootPath, &item.EntryPoint, &item.ContentHash, &item.Status, &item.Error, &created, &updated)
	if err != nil {
		return item, err
	}
	_ = json.Unmarshal([]byte(metadata), &item.Metadata)
	if item.Metadata == nil {
		item.Metadata = map[string]string{}
	}
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

func (s *Store) hydrateSkill(ctx context.Context, item model.Skill) (model.Skill, error) {
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_files WHERE skill_id=?`, item.ID).Scan(&item.FileCount); err != nil {
		return item, err
	}
	for _, relation := range []string{"skill_uses_library", "library_requires_skill"} {
		rows, err := s.DB.QueryContext(ctx, `SELECT library_id FROM skill_library_links WHERE skill_id=? AND relation=? ORDER BY library_id`, item.ID, relation)
		if err != nil {
			return item, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return item, err
			}
			if relation == "skill_uses_library" {
				item.UsesLibraryIDs = append(item.UsesLibraryIDs, id)
			} else {
				item.RequiresLibraryIDs = append(item.RequiresLibraryIDs, id)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return item, err
		}
		rows.Close()
	}
	if item.UsesLibraryIDs == nil {
		item.UsesLibraryIDs = []string{}
	}
	if item.RequiresLibraryIDs == nil {
		item.RequiresLibraryIDs = []string{}
	}
	systemRole, err := s.SystemSkillRole(ctx, item.ID)
	if err != nil {
		return item, err
	}
	item.SystemRole = systemRole
	return item, nil
}

func (s *Store) ListSkills(ctx context.Context) ([]model.Skill, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,description,compatibility,license,metadata_json,allowed_tools,root_path,entry_point,content_hash,status,error,created_at,updated_at FROM skills ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	items := []model.Skill{}
	for rows.Next() {
		item, err := scanSkill(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range items {
		items[i], err = s.hydrateSkill(ctx, items[i])
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) GetSkill(ctx context.Context, id string) (model.Skill, error) {
	item, err := scanSkill(s.DB.QueryRowContext(ctx, `SELECT id,name,description,compatibility,license,metadata_json,allowed_tools,root_path,entry_point,content_hash,status,error,created_at,updated_at FROM skills WHERE id=?`, id))
	if err != nil {
		return item, err
	}
	return s.hydrateSkill(ctx, item)
}

func (s *Store) SkillFiles(ctx context.Context, id string) ([]model.SkillFile, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path,size,sha256,media_type FROM skill_files WHERE skill_id=? ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.SkillFile{}
	for rows.Next() {
		var item model.SkillFile
		if err := rows.Scan(&item.Path, &item.Size, &item.SHA256, &item.MediaType); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ReadSkillFile(ctx context.Context, id, relative string) (string, model.SkillFile, error) {
	item, err := s.GetSkill(ctx, id)
	if err != nil {
		return "", model.SkillFile{}, err
	}
	relative, err = normalizeSkillZipPath(relative)
	if err != nil {
		return "", model.SkillFile{}, err
	}
	var file model.SkillFile
	if err := s.DB.QueryRowContext(ctx, `SELECT path,size,sha256,media_type FROM skill_files WHERE skill_id=? AND path=?`, id, relative).Scan(&file.Path, &file.Size, &file.SHA256, &file.MediaType); err != nil {
		return "", model.SkillFile{}, err
	}
	root, err := s.Resolve(item.RootPath)
	if err != nil {
		return "", model.SkillFile{}, err
	}
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", model.SkillFile{}, errors.New("skill file path escapes root")
	}
	return target, file, nil
}

func (s *Store) SetSkillLinks(ctx context.Context, id string, uses, requires []string) (model.Skill, error) {
	if _, err := s.GetSkill(ctx, id); err != nil {
		return model.Skill{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.Skill{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_library_links WHERE skill_id=?`, id); err != nil {
		return model.Skill{}, err
	}
	seen := map[string]bool{}
	for _, link := range []struct {
		ids      []string
		relation string
	}{{uses, "skill_uses_library"}, {requires, "library_requires_skill"}} {
		for _, libraryID := range link.ids {
			if strings.TrimSpace(libraryID) == "" || seen[link.relation+":"+libraryID] {
				continue
			}
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE id=?`, libraryID).Scan(&exists); err != nil || exists == 0 {
				if err != nil {
					return model.Skill{}, err
				}
				return model.Skill{}, sql.ErrNoRows
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO skill_library_links(skill_id,library_id,relation) VALUES(?,?,?)`, id, libraryID, link.relation); err != nil {
				return model.Skill{}, err
			}
			seen[link.relation+":"+libraryID] = true
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Skill{}, err
	}
	return s.GetSkill(ctx, id)
}

func (s *Store) DeleteSkill(ctx context.Context, id string) error {
	item, err := s.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	systemSkill, err := s.IsSystemSkill(ctx, id)
	if err != nil {
		return err
	}
	if systemSkill {
		return ErrSystemSkillProtected
	}
	result, err := s.DB.ExecContext(ctx, "DELETE FROM skills WHERE id=?", id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	root, err := s.Resolve(item.RootPath)
	if err != nil {
		return err
	}
	return os.RemoveAll(root)
}

func (s *Store) SearchSkills(ctx context.Context, req model.SkillQueryRequest) ([]model.SkillMatch, error) {
	items, err := s.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	results := []model.SkillMatch{}
	for _, item := range items {
		if len(req.LibraryIDs) > 0 && !skillMatchesLibraries(item, req.LibraryIDs) {
			continue
		}
		score := lexicalScore(req.Query, item.Name+" "+item.Description)
		if score == 0 {
			continue
		}
		results = append(results, model.SkillMatch{Skill: item, Score: score})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > req.TopK {
		results = results[:req.TopK]
	}
	return results, nil
}

func skillMatchesLibraries(item model.Skill, libraries []string) bool {
	allowed := map[string]bool{}
	for _, id := range append(append([]string{}, item.UsesLibraryIDs...), item.RequiresLibraryIDs...) {
		allowed[id] = true
	}
	for _, id := range libraries {
		if allowed[id] {
			return true
		}
	}
	return false
}

func (s *Store) RequiredSkills(ctx context.Context, libraries []string) ([]model.Skill, error) {
	if len(libraries) == 0 {
		return []model.Skill{}, nil
	}
	args := make([]any, len(libraries)+1)
	args[0] = "library_requires_skill"
	for i, id := range libraries {
		args[i+1] = id
	}
	query := `SELECT DISTINCT s.id,s.name,s.description,s.compatibility,s.license,s.metadata_json,s.allowed_tools,s.root_path,s.entry_point,s.content_hash,s.status,s.error,s.created_at,s.updated_at FROM skills s JOIN skill_library_links l ON l.skill_id=s.id WHERE l.relation=? AND l.library_id IN (` + strings.TrimRight(strings.Repeat("?,", len(libraries)), ",") + `) ORDER BY lower(s.name)`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	items := []model.Skill{}
	for rows.Next() {
		item, err := scanSkill(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range items {
		items[i], err = s.hydrateSkill(ctx, items[i])
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) PutObject(reader io.Reader) (relative, digest string, err error) {
	temp, err := os.CreateTemp(filepath.Join(s.DataRoot, "staging"), "object-*")
	if err != nil {
		return "", "", err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, h), reader); err != nil {
		temp.Close()
		return "", "", err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return "", "", err
	}
	if err := temp.Close(); err != nil {
		return "", "", err
	}
	digest = hex.EncodeToString(h.Sum(nil))
	relative = filepath.ToSlash(filepath.Join("objects", digest[:2], digest))
	target := filepath.Join(s.DataRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(target); err == nil {
		return relative, digest, nil
	}
	if err := os.Rename(tempName, target); err != nil {
		return "", "", err
	}
	return relative, digest, nil
}

func (s *Store) Resolve(relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("absolute stored paths are forbidden")
	}
	root := filepath.Clean(s.DataRoot)
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes data root")
	}
	return target, nil
}

func (s *Store) CreateJob(ctx context.Context, kind string, payload any) (model.Job, error) {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return model.Job{}, fmt.Errorf("marshal job payload: %w", err)
	}
	t, stamp := now()
	job := model.Job{ID: uuid.NewString(), Kind: kind, Status: "queued", Progress: 0, CreatedAt: t, UpdatedAt: t}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO jobs(id,kind,status,progress,message,payload_json,created_at,updated_at) VALUES(?,?,'queued',0,'',?,?,?)`, job.ID, kind, string(bytes), stamp, stamp)
	return job, err
}

func (s *Store) UpdateJob(ctx context.Context, id, status string, progress float64, message string) error {
	_, stamp := now()
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status=?,progress=?,message=?,updated_at=? WHERE id=?`, status, progress, message, stamp, id)
	return err
}

func (s *Store) ListJobs(ctx context.Context) ([]model.Job, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,kind,status,progress,message,created_at,updated_at FROM jobs ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Job{}
	for rows.Next() {
		var item model.Job
		var created, updated string
		if err := rows.Scan(&item.ID, &item.Kind, &item.Status, &item.Progress, &item.Message, &created, &updated); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListSavedSearches(ctx context.Context) ([]model.SavedSearch, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,query,library_ids_json,tags_json FROM saved_searches ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.SavedSearch{}
	for rows.Next() {
		var item model.SavedSearch
		var libraries, tags string
		if err := rows.Scan(&item.ID, &item.Name, &item.Query, &libraries, &tags); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(libraries), &item.LibraryIDs)
		_ = json.Unmarshal([]byte(tags), &item.Tags)
		if item.LibraryIDs == nil {
			item.LibraryIDs = []string{}
		}
		if item.Tags == nil {
			item.Tags = []string{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateSavedSearch(ctx context.Context, name, query string, libraries, tags []string) (model.SavedSearch, error) {
	name = strings.TrimSpace(name)
	query = strings.TrimSpace(query)
	if name == "" || query == "" {
		return model.SavedSearch{}, errors.New("name and query are required")
	}
	if libraries == nil {
		libraries = []string{}
	}
	if tags == nil {
		tags = []string{}
	}
	libraryJSON, _ := json.Marshal(libraries)
	tagsJSON, _ := json.Marshal(tags)
	item := model.SavedSearch{ID: uuid.NewString(), Name: name, Query: query, LibraryIDs: libraries, Tags: tags}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO saved_searches(id,name,query,library_ids_json,tags_json) VALUES(?,?,?,?,?)`, item.ID, item.Name, item.Query, string(libraryJSON), string(tagsJSON))
	return item, err
}

func (s *Store) DeleteSavedSearch(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM saved_searches WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func lexicalScore(query, text string) float64 {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return 0
	}
	lower := strings.ToLower(text)
	matches := 0
	for _, term := range terms {
		if strings.Contains(lower, term) {
			matches++
		}
	}
	return float64(matches) / float64(len(terms))
}

func (s *Store) Search(ctx context.Context, req model.QueryRequest) ([]model.Evidence, error) {
	query := `SELECT c.id,c.document_id,c.text,c.location_json,c.content_hash,d.library_id,d.title FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.status='ready'`
	args := []any{}
	if len(req.LibraryIDs) > 0 {
		query += ` AND d.library_id IN (` + strings.TrimRight(strings.Repeat("?,", len(req.LibraryIDs)), ",") + `)`
		for _, id := range req.LibraryIDs {
			args = append(args, id)
		}
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Evidence{}
	for rows.Next() {
		var item model.Evidence
		var location string
		if err := rows.Scan(&item.ID, &item.DocumentID, &item.Text, &location, &item.ContentHash, &item.LibraryID, &item.Title); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(location), &item.Location)
		score := lexicalScore(req.Query, item.Text)
		if score == 0 {
			continue
		}
		item.Scores = model.Scores{Lexical: score, Fusion: score, Final: score}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Scores.Final > items[j].Scores.Final })
	if len(items) > req.TopK {
		items = items[:req.TopK]
	}
	return items, rows.Err()
}

// EvidenceByChunkIDs hydrates worker-ranked chunks from the authoritative
// SQLite store. The worker owns ranking/index state; SQLite owns document
// metadata and the final citation payload.
func (s *Store) EvidenceByChunkIDs(ctx context.Context, ids []string) (map[string]model.Evidence, error) {
	items := make(map[string]model.Evidence, len(ids))
	if len(ids) == 0 {
		return items, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id,c.document_id,c.text,c.location_json,c.content_hash,d.library_id,d.title FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.status='ready' AND c.id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item model.Evidence
		var location string
		if err := rows.Scan(&item.ID, &item.DocumentID, &item.Text, &location, &item.ContentHash, &item.LibraryID, &item.Title); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(location), &item.Location)
		items[item.ID] = item
	}
	return items, rows.Err()
}

func (s *Store) CreateToken(ctx context.Context, name, hash string, scopes, libraries []string) (model.AgentToken, error) {
	t, stamp := now()
	scopesJSON, _ := json.Marshal(scopes)
	librariesJSON, _ := json.Marshal(libraries)
	item := model.AgentToken{ID: uuid.NewString(), Name: name, Scopes: scopes, LibraryIDs: libraries, CreatedAt: t}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO agent_tokens(id,name,token_hash,scopes_json,library_ids_json,created_at) VALUES(?,?,?,?,?,?)`, item.ID, name, hash, string(scopesJSON), string(librariesJSON), stamp)
	return item, err
}

func (s *Store) FindToken(ctx context.Context, hash string) (model.AgentToken, error) {
	var item model.AgentToken
	var scopes, libraries, created string
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,scopes_json,library_ids_json,created_at FROM agent_tokens WHERE token_hash=? AND revoked_at IS NULL`, hash).Scan(&item.ID, &item.Name, &scopes, &libraries, &created)
	_ = json.Unmarshal([]byte(scopes), &item.Scopes)
	_ = json.Unmarshal([]byte(libraries), &item.LibraryIDs)
	item.CreatedAt = parseTime(created)
	return item, err
}

func (s *Store) ListTokens(ctx context.Context) ([]model.AgentToken, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,scopes_json,library_ids_json,created_at,revoked_at FROM agent_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.AgentToken{}
	for rows.Next() {
		var item model.AgentToken
		var scopes, libraries, created string
		var revoked sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &scopes, &libraries, &created, &revoked); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopes), &item.Scopes)
		_ = json.Unmarshal([]byte(libraries), &item.LibraryIDs)
		item.CreatedAt = parseTime(created)
		if revoked.Valid {
			v := parseTime(revoked.String)
			item.RevokedAt = &v
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RevokeToken(ctx context.Context, id string) error {
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, `UPDATE agent_tokens SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, stamp, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SaveProvider(ctx context.Context, p model.Provider) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO providers(id,name,kind,base_url,model,embedding_model,local) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,kind=excluded.kind,base_url=excluded.base_url,model=excluded.model,embedding_model=excluded.embedding_model,local=excluded.local`, p.ID, p.Name, p.Kind, p.BaseURL, p.Model, p.EmbeddingModel, boolInt(p.Local))
	return err
}

func (s *Store) ListProviders(ctx context.Context) ([]model.Provider, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,kind,base_url,model,embedding_model,local FROM providers ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Provider{}
	for rows.Next() {
		var p model.Provider
		var local int
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.Model, &p.EmbeddingModel, &local); err != nil {
			return nil, err
		}
		p.Local = local == 1
		items = append(items, p)
	}
	return items, rows.Err()
}

func (s *Store) GetProvider(ctx context.Context, id string) (model.Provider, error) {
	var p model.Provider
	var local int
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,kind,base_url,model,embedding_model,local FROM providers WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.Model, &p.EmbeddingModel, &local)
	p.Local = local == 1
	return p, err
}

func (s *Store) LibrariesAllowRemote(ctx context.Context, ids []string) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	query := `SELECT COUNT(*), COUNT(CASE WHEN allow_remote_models=0 THEN 1 END) FROM libraries WHERE id IN (` + strings.TrimRight(strings.Repeat("?,", len(ids)), ",") + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	var found, denied int
	err := s.DB.QueryRowContext(ctx, query, args...).Scan(&found, &denied)
	return found == len(ids) && denied == 0, err
}

func (s *Store) AddFeedback(ctx context.Context, requestID, chunkID string, relevant bool, note string) error {
	_, stamp := now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO feedback(id,request_id,chunk_id,relevant,note,created_at) VALUES(?,?,?,?,?,?)`, uuid.NewString(), requestID, chunkID, boolInt(relevant), note, stamp)
	return err
}

func (s *Store) RecordQueryEvidence(ctx context.Context, requestID string, evidence []model.Evidence) error {
	if requestID == "" {
		return errors.New("request ID is required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, stamp := now()
	for _, item := range evidence {
		if item.ID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO query_evidence(request_id,chunk_id,created_at) VALUES(?,?,?)`, requestID, item.ID, stamp); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) QueryEvidenceContains(ctx context.Context, requestID, chunkID string) (bool, error) {
	var found int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM query_evidence WHERE request_id=? AND chunk_id=?`, requestID, chunkID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && found == 1, err
}

func (s *Store) ChunkLibrary(ctx context.Context, chunkID string) (string, error) {
	var libraryID string
	err := s.DB.QueryRowContext(ctx, `SELECT d.library_id FROM chunks c JOIN documents d ON d.id=c.document_id WHERE c.id=?`, chunkID).Scan(&libraryID)
	return libraryID, err
}

func (s *Store) AddAudit(ctx context.Context, actor, action, target string, details any) error {
	_, stamp := now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO audit_log(id,actor,action,target,details_json,created_at) VALUES(?,?,?,?,?,?)`, uuid.NewString(), actor, action, target, mustJSON(details), stamp)
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]model.AuditEntry, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,actor,action,target,details_json,created_at FROM audit_log ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.AuditEntry{}
	for rows.Next() {
		var item model.AuditEntry
		var details, created string
		if err := rows.Scan(&item.ID, &item.Actor, &item.Action, &item.Target, &details, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		_ = json.Unmarshal([]byte(details), &item.Details)
		if item.Details == nil {
			item.Details = map[string]any{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) CreateBackup(ctx context.Context, includeIndexes bool) (string, string, error) {
	if _, err := s.DB.ExecContext(ctx, `PRAGMA wal_checkpoint(FULL)`); err != nil {
		return "", "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	name := "knowledge-agent-hub-" + stamp + ".kahbackup"
	target := filepath.Join(s.DataRoot, "backups", name)
	file, err := os.Create(target)
	if err != nil {
		return "", "", err
	}
	zw := zip.NewWriter(file)
	hashes := map[string]string{}
	walkErr := filepath.WalkDir(s.DataRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == s.DataRoot {
			return nil
		}
		rel, _ := filepath.Rel(s.DataRoot, path)
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if strings.HasPrefix(rel, "backups") || strings.HasPrefix(rel, "staging") || (!includeIndexes && strings.HasPrefix(rel, "indexes")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, "-wal") || strings.HasSuffix(rel, "-shm") {
			return nil
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()
		header, err := zip.FileInfoHeader(mustInfo(entry))
		if err != nil {
			return err
		}
		header.Name = "data/" + rel
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		h := sha256.New()
		if _, err := io.Copy(io.MultiWriter(writer, h), source); err != nil {
			return err
		}
		hashes[rel] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	if walkErr == nil {
		manifest, _ := zw.Create("manifest.json")
		_ = json.NewEncoder(manifest).Encode(map[string]any{"format": "kahbackup", "version": 1, "createdAt": time.Now().UTC(), "includeIndexes": includeIndexes, "files": hashes})
	}
	closeErr := zw.Close()
	fileErr := file.Close()
	if walkErr != nil {
		return "", "", walkErr
	}
	if closeErr != nil {
		return "", "", closeErr
	}
	if fileErr != nil {
		return "", "", fileErr
	}
	opened, err := os.Open(target)
	if err != nil {
		return "", "", err
	}
	defer opened.Close()
	h := sha256.New()
	_, err = io.Copy(h, opened)
	return filepath.ToSlash(filepath.Join("backups", name)), hex.EncodeToString(h.Sum(nil)), err
}

func mustInfo(entry fs.DirEntry) fs.FileInfo {
	info, err := entry.Info()
	if err != nil {
		panic(err)
	}
	return info
}

// ListQueuedJobs returns queued jobs with their private replay payloads.
// Payload errors are kept on the individual item so one damaged job cannot
// prevent the remaining queue from recovering.
func (s *Store) ListQueuedJobs(ctx context.Context) ([]QueuedJob, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,kind,status,progress,message,payload_json,created_at,updated_at FROM jobs WHERE status='queued' ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []QueuedJob{}
	for rows.Next() {
		var item QueuedJob
		var payload, created, updated string
		if err := rows.Scan(&item.ID, &item.Kind, &item.Status, &item.Progress, &item.Message, &payload, &created, &updated); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
		item.Payload = map[string]any{}
		if err := json.Unmarshal([]byte(payload), &item.Payload); err != nil {
			item.PayloadError = fmt.Errorf("decode job payload: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ClaimJob atomically moves one queued job to running. The row count makes
// recovery idempotent when startup logic is invoked more than once.
func (s *Store) ClaimJob(ctx context.Context, id string) (bool, error) {
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status='running',message='Resuming after restart',updated_at=? WHERE id=? AND status='queued'`, stamp, id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

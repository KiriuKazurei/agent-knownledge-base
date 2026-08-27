package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/google/uuid"
)

func (s *Store) ListDocumentsFiltered(ctx context.Context, libraryID, folderID string, favorite bool) ([]model.Document, error) {
	query := "SELECT d.id,d.library_id,d.title,d.media_type,d.source_path,d.source_url,d.object_path,d.content_hash,d.status,d.error,d.tags_json,d.favorite,d.created_at,d.updated_at FROM documents d"
	args := []any{}
	conditions := []string{}
	if libraryID != "" {
		conditions = append(conditions, "d.library_id=?")
		args = append(args, libraryID)
	}
	if folderID != "" {
		query += " JOIN document_folders df ON df.document_id=d.id"
		conditions = append(conditions, "df.folder_id=?")
		args = append(args, folderID)
	}
	if favorite {
		conditions = append(conditions, "d.favorite=1")
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY d.updated_at DESC"
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

func (s *Store) ListFolders(ctx context.Context, libraryID string) ([]model.VirtualFolder, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,library_id,name,COALESCE(parent_id,''),created_at,updated_at FROM virtual_folders WHERE library_id=? ORDER BY lower(name)", libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.VirtualFolder{}
	for rows.Next() {
		var item model.VirtualFolder
		var created, updated string
		if err := rows.Scan(&item.ID, &item.LibraryID, &item.Name, &item.ParentID, &created, &updated); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateFolder(ctx context.Context, libraryID, name, parentID string) (model.VirtualFolder, error) {
	name = strings.TrimSpace(name)
	if libraryID == "" || name == "" || len([]rune(name)) > 120 {
		return model.VirtualFolder{}, errors.New("libraryId and a folder name of 1-120 characters are required")
	}
	var libraryCount int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM libraries WHERE id=?", libraryID).Scan(&libraryCount); err != nil || libraryCount == 0 {
		if err != nil {
			return model.VirtualFolder{}, err
		}
		return model.VirtualFolder{}, sql.ErrNoRows
	}
	if parentID != "" {
		var parentLibrary string
		if err := s.DB.QueryRowContext(ctx, "SELECT library_id FROM virtual_folders WHERE id=?", parentID).Scan(&parentLibrary); err != nil {
			return model.VirtualFolder{}, err
		}
		if parentLibrary != libraryID {
			return model.VirtualFolder{}, errors.New("parent folder belongs to another library")
		}
	}
	t, stamp := now()
	item := model.VirtualFolder{ID: uuid.NewString(), LibraryID: libraryID, Name: name, ParentID: parentID, CreatedAt: t, UpdatedAt: t}
	var parent any
	if parentID != "" {
		parent = parentID
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO virtual_folders(id,library_id,name,parent_id,created_at,updated_at) VALUES(?,?,?,?,?,?)", item.ID, item.LibraryID, item.Name, parent, stamp, stamp)
	return item, err
}

func (s *Store) DeleteFolder(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM virtual_folders WHERE id=?", id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetDocumentFolder(ctx context.Context, documentID, folderID string) error {
	var documentLibrary, folderLibrary string
	if err := s.DB.QueryRowContext(ctx, "SELECT library_id FROM documents WHERE id=?", documentID).Scan(&documentLibrary); err != nil {
		return err
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT library_id FROM virtual_folders WHERE id=?", folderID).Scan(&folderLibrary); err != nil {
		return err
	}
	if documentLibrary != folderLibrary {
		return errors.New("document and folder belong to different libraries")
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO document_folders(document_id,folder_id) VALUES(?,?) ON CONFLICT(document_id,folder_id) DO NOTHING", documentID, folderID)
	return err
}

func (s *Store) RemoveDocumentFolder(ctx context.Context, documentID, folderID string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM document_folders WHERE document_id=? AND folder_id=?", documentID, folderID)
	return err
}

func (s *Store) ListSourceWatches(ctx context.Context, libraryID string) ([]model.SourceWatch, error) {
	query := "SELECT id,library_id,root_path,recursive,enabled,last_scan_at,last_message,created_at,updated_at FROM source_watches"
	args := []any{}
	if libraryID != "" {
		query += " WHERE library_id=?"
		args = append(args, libraryID)
	}
	query += " ORDER BY created_at DESC"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.SourceWatch{}
	for rows.Next() {
		item, err := scanSourceWatch(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetSourceWatch(ctx context.Context, id string) (model.SourceWatch, error) {
	return scanSourceWatch(s.DB.QueryRowContext(ctx, "SELECT id,library_id,root_path,recursive,enabled,last_scan_at,last_message,created_at,updated_at FROM source_watches WHERE id=?", id))
}

func scanSourceWatch(row rowScanner) (model.SourceWatch, error) {
	var item model.SourceWatch
	var recursive, enabled int
	var lastScan, created, updated sql.NullString
	if err := row.Scan(&item.ID, &item.LibraryID, &item.RootPath, &recursive, &enabled, &lastScan, &item.LastMessage, &created, &updated); err != nil {
		return item, err
	}
	item.Recursive, item.Enabled = recursive == 1, enabled == 1
	if lastScan.Valid && lastScan.String != "" {
		value := parseTime(lastScan.String)
		item.LastScanAt = &value
	}
	item.CreatedAt, item.UpdatedAt = parseTime(created.String), parseTime(updated.String)
	return item, nil
}

func (s *Store) CreateSourceWatch(ctx context.Context, libraryID, rootPath string, recursive bool) (model.SourceWatch, error) {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if libraryID == "" || rootPath == "" || rootPath == "." {
		return model.SourceWatch{}, errors.New("libraryId and rootPath are required")
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return model.SourceWatch{}, err
	}
	stat, err := os.Stat(absolute)
	if err != nil || !stat.IsDir() {
		if err == nil {
			err = errors.New("rootPath must be a directory")
		}
		return model.SourceWatch{}, err
	}
	var libraryCount int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM libraries WHERE id=?", libraryID).Scan(&libraryCount); err != nil || libraryCount == 0 {
		if err != nil {
			return model.SourceWatch{}, err
		}
		return model.SourceWatch{}, sql.ErrNoRows
	}
	t, stamp := now()
	item := model.SourceWatch{ID: uuid.NewString(), LibraryID: libraryID, RootPath: absolute, Recursive: recursive, Enabled: true, CreatedAt: t, UpdatedAt: t}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO source_watches(id,library_id,root_path,recursive,enabled,last_message,created_at,updated_at) VALUES(?,?,?,?,1,'',?,?)", item.ID, item.LibraryID, item.RootPath, boolInt(item.Recursive), stamp, stamp)
	return item, err
}

func (s *Store) UpdateSourceWatchScan(ctx context.Context, id, message string) error {
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, "UPDATE source_watches SET last_scan_at=?,last_message=?,updated_at=? WHERE id=?", stamp, message, stamp, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteSourceWatch(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM source_watches WHERE id=?", id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) FindDocumentByHash(ctx context.Context, libraryID, hash string) (model.Document, error) {
	row := s.DB.QueryRowContext(ctx, "SELECT id,library_id,title,media_type,source_path,source_url,object_path,content_hash,status,error,tags_json,favorite,created_at,updated_at FROM documents WHERE library_id=? AND content_hash=? LIMIT 1", libraryID, hash)
	return scanDocument(row)
}
func (s *Store) ChunksForLibrary(ctx context.Context, libraryID string) ([]model.Chunk, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT c.id,c.document_id,c.text,c.location_json,c.content_hash FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.library_id=? AND d.status='ready' ORDER BY c.document_id,c.ordinal", libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Chunk{}
	for rows.Next() {
		var item model.Chunk
		var location string
		if err := rows.Scan(&item.ID, &item.DocumentID, &item.Text, &location, &item.ContentHash); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(location), &item.Location)
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) FindDocumentBySourcePath(ctx context.Context, libraryID, sourcePath string) (model.Document, error) {
	row := s.DB.QueryRowContext(ctx, "SELECT id,library_id,title,media_type,source_path,source_url,object_path,content_hash,status,error,tags_json,favorite,created_at,updated_at FROM documents WHERE library_id=? AND source_path=? LIMIT 1", libraryID, sourcePath)
	return scanDocument(row)
}

func (s *Store) ListDocumentsWithSources(ctx context.Context, libraryID string) ([]model.Document, error) {
	return s.ListDocumentsFiltered(ctx, libraryID, "", false)
}

func (s *Store) MarkDocumentSourceMissing(ctx context.Context, id string) error {
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, `UPDATE documents SET status='source_missing',error='Source file is missing',updated_at=? WHERE id=? AND source_path<>''`, stamp, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ValidateLibraryIDs(ctx context.Context, ids []string) error {
	unique := map[string]bool{}
	values := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !unique[id] {
			unique[id] = true
			values = append(values, id)
		}
	}
	if len(values) == 0 {
		return nil
	}
	args := make([]any, len(values))
	for i, id := range values {
		args[i] = id
	}
	query := `SELECT COUNT(*) FROM libraries WHERE id IN (` + strings.TrimRight(strings.Repeat("?,", len(values)), ",") + `)`
	var found int
	if err := s.DB.QueryRowContext(ctx, query, args...).Scan(&found); err != nil {
		return err
	}
	if found != len(values) {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SkillAccessibleToLibraries(ctx context.Context, skillID string, libraryIDs []string) (bool, error) {
	if len(libraryIDs) == 0 {
		return true, nil
	}
	args := make([]any, 0, len(libraryIDs)+3)
	args = append(args, skillID, "skill_uses_library", "library_requires_skill")
	for _, id := range libraryIDs {
		args = append(args, id)
	}
	query := `SELECT COUNT(*) FROM skill_library_links WHERE skill_id=? AND relation IN (?,?) AND library_id IN (` + strings.TrimRight(strings.Repeat("?,", len(libraryIDs)), ",") + `)`
	var count int
	if err := s.DB.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}
func (s *Store) UpdateDocumentSourcePath(ctx context.Context, id, sourcePath string) error {
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, `UPDATE documents SET source_path=?,status='ready',error='',updated_at=? WHERE id=?`, sourcePath, stamp, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

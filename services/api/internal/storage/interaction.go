package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/google/uuid"
)

func (s *Store) ListTrash(ctx context.Context) ([]model.TrashEntry, error) {
	items := []model.TrashEntry{}
	rows, err := s.DB.QueryContext(ctx, `SELECT 'library',l.id,'',l.name,l.description,l.deleted_at,(SELECT COUNT(*) FROM documents d WHERE d.library_id=l.id) FROM libraries l WHERE l.deleted_at IS NOT NULL
		UNION ALL
		SELECT 'document',d.id,d.library_id,d.title,d.media_type,d.deleted_at,0 FROM documents d JOIN libraries l ON l.id=d.library_id WHERE d.deleted_at IS NOT NULL AND l.deleted_at IS NULL
		ORDER BY deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item model.TrashEntry
		var deleted string
		if err := rows.Scan(&item.Kind, &item.ID, &item.LibraryID, &item.Name, &item.Description, &deleted, &item.ItemCount); err != nil {
			return nil, err
		}
		item.DeletedAt = parseTime(deleted)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RestoreTrash(ctx context.Context, kind, id string) error {
	_, stamp := now()
	var result sql.Result
	var err error
	switch kind {
	case "library":
		result, err = s.DB.ExecContext(ctx, `UPDATE libraries SET deleted_at=NULL,updated_at=? WHERE id=? AND deleted_at IS NOT NULL`, stamp, id)
	case "document":
		result, err = s.DB.ExecContext(ctx, `UPDATE documents SET deleted_at=NULL,updated_at=? WHERE id=? AND deleted_at IS NOT NULL AND EXISTS (SELECT 1 FROM libraries l WHERE l.id=documents.library_id AND l.deleted_at IS NULL)`, stamp, id)
	default:
		return errors.New("trash kind must be library or document")
	}
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) PurgeTrash(ctx context.Context, kind, id string) error {
	paths := []string{}
	query := `SELECT d.object_path FROM documents d JOIN libraries l ON l.id=d.library_id WHERE d.deleted_at IS NOT NULL OR l.deleted_at IS NOT NULL`
	args := []any{}
	if kind == "library" {
		query = `SELECT d.object_path FROM documents d WHERE d.library_id=? AND EXISTS (SELECT 1 FROM libraries l WHERE l.id=? AND l.deleted_at IS NOT NULL)`
		args = []any{id, id}
	} else if kind == "document" {
		query = `SELECT d.object_path FROM documents d JOIN libraries l ON l.id=d.library_id WHERE d.deleted_at IS NOT NULL AND l.deleted_at IS NULL AND d.id=?`
		args = []any{id}
	} else if kind != "all" {
		return errors.New("trash kind must be library, document, or all")
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return err
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var result sql.Result
	if kind == "library" {
		result, err = s.DB.ExecContext(ctx, `DELETE FROM libraries WHERE id=? AND deleted_at IS NOT NULL`, id)
	} else if kind == "document" {
		result, err = s.DB.ExecContext(ctx, `DELETE FROM documents WHERE id=? AND deleted_at IS NOT NULL`, id)
	} else {
		// A trashed library keeps its documents' deleted_at value intact so that
		// restoring the library can restore the document set as-is. Purging all
		// trash must therefore remove documents that are hidden by either their
		// own tombstone or their library's tombstone before deleting libraries.
		if _, err = s.DB.ExecContext(ctx, `DELETE FROM documents WHERE deleted_at IS NOT NULL OR library_id IN (SELECT id FROM libraries WHERE deleted_at IS NOT NULL)`); err == nil {
			result, err = s.DB.ExecContext(ctx, `DELETE FROM libraries WHERE deleted_at IS NOT NULL`)
		}
	}
	if err != nil {
		return err
	}
	if kind != "all" {
		count, _ := result.RowsAffected()
		if count == 0 {
			return sql.ErrNoRows
		}
	}
	for _, relative := range paths {
		var references int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents WHERE object_path=?`, relative).Scan(&references); err == nil && references == 0 {
			if target, resolveErr := s.Resolve(relative); resolveErr == nil {
				_ = os.Remove(target)
			}
		}
	}
	return nil
}

func (s *Store) UpdateFolder(ctx context.Context, id, name, parentID string) (model.VirtualFolder, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 120 {
		return model.VirtualFolder{}, errors.New("folder name must contain 1-120 characters")
	}
	var item model.VirtualFolder
	var currentParent, created, updated string
	if err := s.DB.QueryRowContext(ctx, `SELECT f.id,f.library_id,f.name,COALESCE(f.parent_id,''),f.created_at,f.updated_at FROM virtual_folders f JOIN libraries l ON l.id=f.library_id WHERE f.id=? AND l.deleted_at IS NULL`, id).Scan(&item.ID, &item.LibraryID, &item.Name, &currentParent, &created, &updated); err != nil {
		return item, err
	}
	if parentID == id {
		return item, errors.New("folder cannot be its own parent")
	}
	if parentID != "" {
		var parentLibrary string
		if err := s.DB.QueryRowContext(ctx, `SELECT f.library_id FROM virtual_folders f JOIN libraries l ON l.id=f.library_id WHERE f.id=? AND l.deleted_at IS NULL`, parentID).Scan(&parentLibrary); err != nil {
			return item, err
		}
		if parentLibrary != item.LibraryID {
			return item, errors.New("parent folder belongs to another library")
		}
		for cursor := parentID; cursor != ""; {
			if cursor == id {
				return item, errors.New("folder hierarchy cannot contain a cycle")
			}
			var next sql.NullString
			if err := s.DB.QueryRowContext(ctx, `SELECT parent_id FROM virtual_folders WHERE id=?`, cursor).Scan(&next); err != nil {
				return item, err
			}
			cursor = next.String
		}
	}
	_, stamp := now()
	var parent any
	if parentID != "" {
		parent = parentID
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE virtual_folders SET name=?,parent_id=?,updated_at=? WHERE id=?`, name, parent, stamp, id); err != nil {
		return item, err
	}
	item.Name, item.ParentID = name, parentID
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(stamp)
	return item, nil
}

func (s *Store) UpdateSourceWatch(ctx context.Context, id string, enabled, recursive *bool) (model.SourceWatch, error) {
	item, err := s.GetSourceWatch(ctx, id)
	if err != nil {
		return item, err
	}
	if enabled != nil {
		item.Enabled = *enabled
	}
	if recursive != nil {
		item.Recursive = *recursive
	}
	_, stamp := now()
	if _, err := s.DB.ExecContext(ctx, `UPDATE source_watches SET enabled=?,recursive=?,updated_at=? WHERE id=?`, boolInt(item.Enabled), boolInt(item.Recursive), stamp, id); err != nil {
		return item, err
	}
	item.UpdatedAt = parseTime(stamp)
	return item, nil
}

func (s *Store) BatchDocuments(ctx context.Context, input model.DocumentBatchRequest) (model.DocumentBatchResult, error) {
	ids := uniqueStrings(input.DocumentIDs)
	if len(ids) == 0 || len(ids) > 1000 {
		return model.DocumentBatchResult{}, errors.New("documentIds must contain 1-1000 unique values")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.DocumentBatchResult{}, err
	}
	defer tx.Rollback()
	libraryID := ""
	for _, id := range ids {
		var candidate string
		if err := tx.QueryRowContext(ctx, `SELECT d.library_id FROM documents d JOIN libraries l ON l.id=d.library_id WHERE d.id=? AND d.deleted_at IS NULL AND l.deleted_at IS NULL`, id).Scan(&candidate); err != nil {
			return model.DocumentBatchResult{}, err
		}
		if libraryID == "" {
			libraryID = candidate
		} else if libraryID != candidate {
			return model.DocumentBatchResult{}, errors.New("all documents must belong to the same library")
		}
	}
	_, stamp := now()
	for _, id := range ids {
		switch input.Action {
		case "favorite":
			if input.Favorite == nil {
				return model.DocumentBatchResult{}, errors.New("favorite action requires favorite")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE documents SET favorite=?,updated_at=? WHERE id=?`, boolInt(*input.Favorite), stamp, id); err != nil {
				return model.DocumentBatchResult{}, err
			}
		case "add_tags", "remove_tags":
			var raw string
			if err := tx.QueryRowContext(ctx, `SELECT tags_json FROM documents WHERE id=?`, id).Scan(&raw); err != nil {
				return model.DocumentBatchResult{}, err
			}
			var tags []string
			_ = json.Unmarshal([]byte(raw), &tags)
			if input.Action == "add_tags" {
				tags = uniqueStrings(append(tags, input.Tags...))
			} else {
				remove := stringSet(input.Tags)
				filtered := tags[:0]
				for _, tag := range tags {
					if !remove[tag] {
						filtered = append(filtered, tag)
					}
				}
				tags = filtered
			}
			sort.Strings(tags)
			encoded, _ := json.Marshal(tags)
			if _, err := tx.ExecContext(ctx, `UPDATE documents SET tags_json=?,updated_at=? WHERE id=?`, string(encoded), stamp, id); err != nil {
				return model.DocumentBatchResult{}, err
			}
		case "add_folders", "remove_folders":
			for _, folderID := range uniqueStrings(input.FolderIDs) {
				var folderLibrary string
				if err := tx.QueryRowContext(ctx, `SELECT library_id FROM virtual_folders WHERE id=?`, folderID).Scan(&folderLibrary); err != nil {
					return model.DocumentBatchResult{}, err
				}
				if folderLibrary != libraryID {
					return model.DocumentBatchResult{}, errors.New("folder belongs to another library")
				}
				if input.Action == "add_folders" {
					_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO document_folders(document_id,folder_id) VALUES(?,?)`, id, folderID)
				} else {
					_, err = tx.ExecContext(ctx, `DELETE FROM document_folders WHERE document_id=? AND folder_id=?`, id, folderID)
				}
				if err != nil {
					return model.DocumentBatchResult{}, err
				}
			}
		case "trash":
			if _, err := tx.ExecContext(ctx, `UPDATE documents SET deleted_at=?,updated_at=? WHERE id=?`, stamp, stamp, id); err != nil {
				return model.DocumentBatchResult{}, err
			}
		case "reindex":
			if _, err := tx.ExecContext(ctx, `UPDATE documents SET status='pending',error='',updated_at=? WHERE id=?`, stamp, id); err != nil {
				return model.DocumentBatchResult{}, err
			}
		default:
			return model.DocumentBatchResult{}, fmt.Errorf("unsupported batch action %q", input.Action)
		}
	}
	if err := tx.Commit(); err != nil {
		return model.DocumentBatchResult{}, err
	}
	return model.DocumentBatchResult{LibraryID: libraryID, UpdatedCount: len(ids), DocumentIDs: ids}, nil
}

func (s *Store) RelinkDocumentSource(ctx context.Context, id, sourcePath string) (model.DocumentDetail, error) {
	absolute, err := filepathAbsExistingFile(sourcePath)
	if err != nil {
		return model.DocumentDetail{}, err
	}
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, `UPDATE documents SET source_path=?,status='pending',error='',updated_at=? WHERE id=? AND deleted_at IS NULL`, absolute, stamp, id)
	if err != nil {
		return model.DocumentDetail{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return model.DocumentDetail{}, sql.ErrNoRows
	}
	return s.GetDocument(ctx, id)
}

func filepathAbsExistingFile(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("sourcePath is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("sourcePath must be a file")
	}
	return absolute, nil
}

func (s *Store) UpdateSavedSearch(ctx context.Context, input model.SavedSearch) (model.SavedSearch, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Query = strings.TrimSpace(input.Query)
	if input.ID == "" || input.Name == "" || len([]rune(input.Name)) > 120 || len(input.Query) > 8000 {
		return model.SavedSearch{}, errors.New("saved search id, name, and a valid query are required")
	}
	if input.View != "knowledge" {
		input.View = "sources"
	}
	input.LibraryIDs, input.Tags = uniqueStrings(input.LibraryIDs), uniqueStrings(input.Tags)
	if input.Filters.Tags == nil {
		input.Filters.Tags = append([]string{}, input.Tags...)
	} else {
		input.Filters.Tags = uniqueStrings(input.Filters.Tags)
		input.Tags = append([]string{}, input.Filters.Tags...)
	}
	if input.Filters.FavoritesOnly {
		input.Filters.Favorite = true
	}
	if input.Filters.TagMode != "any" {
		input.Filters.TagMode = "all"
	}
	if input.Sort.Field == "" {
		input.Sort = model.SavedSearchSort{Field: "updatedAt", Direction: "desc"}
	}
	libraries, _ := json.Marshal(input.LibraryIDs)
	tags, _ := json.Marshal(input.Tags)
	filters, _ := json.Marshal(input.Filters)
	sortValue, _ := json.Marshal(input.Sort)
	result, err := s.DB.ExecContext(ctx, `UPDATE saved_searches SET name=?,query=?,view=?,library_ids_json=?,tags_json=?,filters_json=?,sort_json=? WHERE id=?`, input.Name, input.Query, input.View, string(libraries), string(tags), string(filters), string(sortValue), input.ID)
	if err != nil {
		return model.SavedSearch{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return model.SavedSearch{}, sql.ErrNoRows
	}
	return input, nil
}

func (s *Store) CloneJobForRetry(ctx context.Context, id string) (model.Job, error) {
	var kind, status, payload string
	if err := s.DB.QueryRowContext(ctx, `SELECT kind,status,payload_json FROM jobs WHERE id=?`, id).Scan(&kind, &status, &payload); err != nil {
		return model.Job{}, err
	}
	if status != "failed" && status != "cancelled" {
		return model.Job{}, errors.New("only failed or cancelled jobs can be retried")
	}
	t, stamp := now()
	item := model.Job{ID: uuid.NewString(), Kind: kind, Status: "queued", OriginJobID: id, CanCancel: true, CreatedAt: t, UpdatedAt: t}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO jobs(id,kind,status,progress,message,payload_json,origin_job_id,created_at,updated_at) VALUES(?,?,'queued',0,'',?,?,?,?)`, item.ID, kind, payload, id, stamp, stamp)
	return item, err
}

func (s *Store) CancelJob(ctx context.Context, id string) (model.Job, error) {
	var status string
	if err := s.DB.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, id).Scan(&status); err != nil {
		return model.Job{}, err
	}
	if status != "queued" && status != "running" {
		return model.Job{}, errors.New("only queued or running jobs can be cancelled")
	}
	newStatus := "cancelled"
	message := "Cancelled by user"
	if status == "running" {
		newStatus, message = "cancellation_requested", "Cancellation requested by user"
	}
	if err := s.UpdateJob(ctx, id, newStatus, 0, message); err != nil {
		return model.Job{}, err
	}
	items, err := s.ListJobs(ctx)
	if err != nil {
		return model.Job{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return model.Job{}, sql.ErrNoRows
}

func (s *Store) ProviderDependencies(ctx context.Context, id string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT name FROM libraries WHERE deleted_at IS NULL AND (summary_provider_id=? OR review_provider_id=?) ORDER BY lower(name)`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	dependencies, err := s.ProviderDependencies(ctx, id)
	if err != nil {
		return err
	}
	if len(dependencies) > 0 {
		return fmt.Errorf("provider is used by libraries: %s", strings.Join(dependencies, ", "))
	}
	result, err := s.DB.ExecContext(ctx, `DELETE FROM providers WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[strings.TrimSpace(value)] = true
	}
	return result
}

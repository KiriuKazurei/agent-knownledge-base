package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestMigrationAddsInteractionColumnsWithoutRebuildingExistingData(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	library, err := store.CreateLibrary(ctx, "Existing v0.1 data", "must survive")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`DROP INDEX IF EXISTS idx_libraries_deleted`,
		`DROP INDEX IF EXISTS idx_documents_deleted`,
		`ALTER TABLE libraries DROP COLUMN deleted_at`,
		`ALTER TABLE documents DROP COLUMN deleted_at`,
		`ALTER TABLE jobs DROP COLUMN origin_job_id`,
		`ALTER TABLE saved_searches DROP COLUMN view`,
		`ALTER TABLE saved_searches DROP COLUMN filters_json`,
		`ALTER TABLE saved_searches DROP COLUMN sort_json`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			db.Close()
			t.Fatalf("prepare legacy schema %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	preserved, err := reopened.GetLibrary(ctx, library.ID)
	if err != nil || preserved.Name != library.Name || preserved.Description != library.Description {
		t.Fatalf("legacy data was not preserved: %+v, %v", preserved, err)
	}
	for table, columns := range map[string][]string{"libraries": {"deleted_at"}, "documents": {"deleted_at"}, "jobs": {"origin_job_id"}, "saved_searches": {"view", "filters_json", "sort_json"}} {
		for _, column := range columns {
			var count int
			if err := reopened.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); err != nil || count != 1 {
				t.Fatalf("missing migrated column %s.%s: count=%d err=%v", table, column, count, err)
			}
		}
	}
}

func TestTrashLibraryPausesWatchAndRestoreKeepsItPaused(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Trash lifecycle", "")
	if err != nil {
		t.Fatal(err)
	}
	watch, err := store.CreateSourceWatch(ctx, library.ID, t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLibrary(ctx, library.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("trashed library remained visible: %v", err)
	}
	var enabled int
	if err := store.DB.QueryRowContext(ctx, `SELECT enabled FROM source_watches WHERE id=?`, watch.ID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 {
		t.Fatal("source watch was not paused")
	}
	if err := store.RestoreTrash(ctx, "library", library.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRowContext(ctx, `SELECT enabled FROM source_watches WHERE id=?`, watch.ID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 {
		t.Fatal("restoring a library unexpectedly re-enabled its watch")
	}
}

func TestBatchDocumentsRejectsCrossLibrarySelectionWithoutPartialChanges(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, _ := store.CreateLibrary(ctx, "First", "")
	second, _ := store.CreateLibrary(ctx, "Second", "")
	firstDoc, _ := store.CreatePendingDocument(ctx, first.ID, "first.md", "text/markdown", "", "", "objects/first", "first")
	secondDoc, _ := store.CreatePendingDocument(ctx, second.ID, "second.md", "text/markdown", "", "", "objects/second", "second")
	_, err = store.BatchDocuments(ctx, model.DocumentBatchRequest{DocumentIDs: []string{firstDoc.ID, secondDoc.ID}, Action: "add_tags", Tags: []string{"must-not-commit"}})
	if err == nil {
		t.Fatal("cross-library batch unexpectedly succeeded")
	}
	for _, id := range []string{firstDoc.ID, secondDoc.ID} {
		detail, getErr := store.GetDocument(ctx, id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if len(detail.Tags) != 0 {
			t.Fatalf("batch partially changed %s: %#v", id, detail.Tags)
		}
	}
}

func TestDocumentCanBelongToMultipleFoldersAndFolderDeleteOnlyUnassigns(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, _ := store.CreateLibrary(ctx, "Folders", "")
	document, _ := store.CreatePendingDocument(ctx, library.ID, "note.md", "text/markdown", "", "", "objects/note", "note")
	first, _ := store.CreateFolder(ctx, library.ID, "First", "")
	second, _ := store.CreateFolder(ctx, library.ID, "Second", "")
	nested, _ := store.CreateFolder(ctx, library.ID, "Nested", first.ID)
	if _, err := store.BatchDocuments(ctx, model.DocumentBatchRequest{DocumentIDs: []string{document.ID}, Action: "add_folders", FolderIDs: []string{first.ID, second.ID}}); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetDocument(ctx, document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.FolderIDs) != 2 {
		t.Fatalf("expected two folder memberships, got %#v", detail.FolderIDs)
	}
	if err := store.DeleteFolder(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	detail, err = store.GetDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("deleting a folder deleted its document: %v", err)
	}
	if len(detail.FolderIDs) != 1 || detail.FolderIDs[0] != second.ID {
		t.Fatalf("unexpected remaining folders: %#v", detail.FolderIDs)
	}
	folders, err := store.ListFolders(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range folders {
		if folder.ID == nested.ID && folder.ParentID != "" {
			t.Fatalf("nested folder was deleted or kept an invalid parent: %+v", folder)
		}
	}
}

func TestPermanentPurgeKeepsSharedObjectUntilLastReference(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, _ := store.CreateLibrary(ctx, "Objects", "")
	objectPath, hash, err := store.PutObject(strings.NewReader("shared object"))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := store.CreatePendingDocument(ctx, library.ID, "first.md", "text/markdown", "", "", objectPath, hash)
	_, _ = store.CreatePendingDocument(ctx, library.ID, "second.md", "text/markdown", "", "", objectPath, hash)
	if err := store.DeleteDocument(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.PurgeTrash(ctx, "document", first.ID); err != nil {
		t.Fatal(err)
	}
	absolute, err := store.Resolve(objectPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absolute); err != nil {
		t.Fatalf("shared object was removed too early: %v", err)
	}
}

func TestPurgeAllRemovesDocumentsHiddenByTrashedLibrary(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Purge all", "")
	if err != nil {
		t.Fatal(err)
	}
	document, err := store.CreatePendingDocument(ctx, library.ID, "hidden.md", "text/markdown", "", "", "objects/hidden", "hidden")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.PurgeTrash(ctx, "all", ""); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents WHERE id=?`, document.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("document belonging to trashed library survived purge: %d", count)
	}
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE id=?`, library.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("trashed library survived purge: %d", count)
	}
}

func TestJobCancelAndRetryRetainsOrigin(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	job, err := store.CreateJob(ctx, "file_import", map[string]any{"paths": []string{"missing.md"}})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.CancelJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" || !cancelled.CanRetry {
		t.Fatalf("unexpected cancelled job: %+v", cancelled)
	}
	retried, err := store.CloneJobForRetry(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.OriginJobID != job.ID || retried.Status != "queued" {
		t.Fatalf("retry lost origin: %+v", retried)
	}
}

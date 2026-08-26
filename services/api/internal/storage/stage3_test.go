package storage

import (
	"context"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStage3FoldersFavoritesAndSourceWatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Stage3", "")
	if err != nil {
		t.Fatal(err)
	}
	folder, err := store.CreateFolder(ctx, library.ID, "Research", "")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.CreatePendingDocument(ctx, library.ID, "note.md", "text/markdown", "note.md", "", "objects/x", "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceChunks(ctx, doc.ID, []model.Chunk{{ID: "chunk-1", DocumentID: doc.ID, Text: "content", Location: map[string]any{}, ContentHash: "hash-1"}}); err != nil {
		t.Fatal(err)
	}
	favorite := true
	if _, err := store.UpdateDocument(ctx, doc.ID, nil, []string{"research"}, &favorite); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDocumentFolder(ctx, doc.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListDocumentsFiltered(ctx, library.ID, folder.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].Favorite || len(items[0].Tags) != 1 {
		t.Fatalf("unexpected filtered documents: %#v", items)
	}
	watchRoot := filepath.Join(root, "watched")
	if err := os.MkdirAll(watchRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	watch, err := store.CreateSourceWatch(ctx, library.ID, watchRoot, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSourceWatchScan(ctx, watch.ID, "Scanned 1 files"); err != nil {
		t.Fatal(err)
	}
	watches, err := store.ListSourceWatches(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 1 || watches[0].LastMessage != "Scanned 1 files" {
		t.Fatalf("unexpected watches: %#v", watches)
	}
}

func TestStage3ContentUpdateKeepsDocumentAndChunksConsistent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Content", "")
	if err != nil {
		t.Fatal(err)
	}
	firstPath, firstHash, err := store.PutObject(strings.NewReader("old content"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.CreatePendingDocument(ctx, library.ID, "note.md", "text/markdown", "note.md", "", firstPath, firstHash)
	if err != nil {
		t.Fatal(err)
	}
	secondPath, secondHash, err := store.PutObject(strings.NewReader("new content"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDocumentContent(ctx, doc.ID, secondPath, secondHash); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceChunks(ctx, doc.ID, []model.Chunk{{ID: "new-chunk", DocumentID: doc.ID, Text: "new content", ContentHash: secondHash}}); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetDocument(ctx, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ObjectPath != secondPath || detail.ContentHash != secondHash || detail.Status != "ready" || len(detail.Preview) != 1 || detail.Preview[0].Text != "new content" {
		t.Fatalf("content update is inconsistent: %+v", detail)
	}
}

package storage

import (
	"context"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestListDocumentChunksReadsBeyondPreviewPage(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, err := store.CreateLibrary(ctx, "chunk paging", "")
	if err != nil {
		t.Fatal(err)
	}
	document, err := store.CreatePendingDocument(ctx, library.ID, "long.md", "text/markdown", "long.md", "", "objects/long", "hash")
	if err != nil {
		t.Fatal(err)
	}
	chunks := make([]model.Chunk, 0, 3)
	for index := 0; index < 3; index++ {
		chunks = append(chunks, model.Chunk{ID: "chunk-" + string(rune('1'+index)), DocumentID: document.ID, Text: "part", Location: map[string]any{"ordinal": index}, ContentHash: "hash"})
	}
	if err := store.ReplaceChunks(ctx, document.ID, chunks); err != nil {
		t.Fatal(err)
	}
	first, err := store.ListDocumentChunks(ctx, document.ID, 0, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %d, err=%v", len(first), err)
	}
	second, err := store.ListDocumentChunks(ctx, document.ID, 2, 2)
	if err != nil || len(second) != 1 || second[0].ID != "chunk-3" {
		t.Fatalf("second page = %#v, err=%v", second, err)
	}
	if _, err := store.ListDocumentChunks(ctx, document.ID, -1, 2); err == nil {
		t.Fatal("negative offset should be rejected")
	}
}

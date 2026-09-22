package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestBuildRetrievalCorpusExcludesPendingDeletedAndCrossLibraryRows(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	first, err := store.CreateLibrary(ctx, "Retrieval one", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateLibrary(ctx, "Retrieval two", "")
	if err != nil {
		t.Fatal(err)
	}
	ready, err := store.CreatePendingDocument(ctx, first.ID, "ready.md", "text/markdown", "", "", "", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceChunks(ctx, ready.ID, []model.Chunk{{ID: "ready-chunk", DocumentID: ready.ID, Text: "library one ready source", ContentHash: "source-hash"}}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.CreatePendingDocument(ctx, first.ID, "deleted.md", "text/markdown", "", "", "", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceChunks(ctx, deleted.ID, []model.Chunk{{ID: "deleted-chunk", DocumentID: deleted.ID, Text: "must be deleted", ContentHash: "source-hash"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDocument(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := store.CreatePendingDocument(ctx, first.ID, "pending.md", "text/markdown", "", "", "", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO chunks(id,document_id,ordinal,text,location_json,content_hash) VALUES(?,?,?,?,?,?)`, "pending-chunk", pending.ID, 0, "pending source", "{}", "source-hash"); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreatePendingDocument(ctx, second.ID, "other.md", "text/markdown", "", "", "", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceChunks(ctx, other.ID, []model.Chunk{{ID: "other-chunk", DocumentID: other.ID, Text: "library two source", ContentHash: "source-hash"}}); err != nil {
		t.Fatal(err)
	}

	items, err := store.BuildRetrievalCorpus(ctx, []string{first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "ready-chunk" || items[0].LibraryID != first.ID || items[0].SourceType != "raw" {
		t.Fatalf("unexpected scoped corpus: %+v", items)
	}
	if _, err := store.BuildRetrievalCorpus(ctx, nil); err == nil {
		t.Fatal("empty retrieval scope must fail closed")
	}
}

func TestBuildRetrievalCorpusIncludesOnlyStableKnowledge(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, err := store.CreateLibrary(ctx, "Stable retrieval", "")
	if err != nil {
		t.Fatal(err)
	}
	stablePayload := model.KnowledgePayload{Schema: "kah.knowledge.v1", ID: "kah://knowledge/stable", Title: "稳定知识", Description: "稳定描述", Language: "zh-CN", Sections: []model.KnowledgeSection{{ID: "core", Heading: "核心", Content: "正式知识正文"}}}
	payload, _ := json.Marshal(stablePayload)
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO knowledge_items(id,library_id,stable_revision,created_at,updated_at) VALUES(?,?,?,?,?)`, "stable-item", library.ID, 1, "2026-09-21T00:00:00Z", "2026-09-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO knowledge_revisions(knowledge_id,revision,payload_json,markdown,content_hash,status,flags_json,submitted_by,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, "stable-item", 1, string(payload), strings.TrimSpace(stablePayload.Description), "stable-hash", "stable", "[]", "test", "2026-09-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO knowledge_items(id,library_id,stable_revision,created_at,updated_at) VALUES(?,?,?,?,?)`, "draft-item", library.ID, nil, "2026-09-21T00:00:00Z", "2026-09-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	draftPayload := stablePayload
	draftPayload.ID = "kah://knowledge/draft"
	draftPayload.Title = "草稿知识"
	draft, _ := json.Marshal(draftPayload)
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO knowledge_revisions(knowledge_id,revision,payload_json,markdown,content_hash,status,flags_json,submitted_by,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, "draft-item", 1, string(draft), "草稿", "draft-hash", "draft", "[]", "test", "2026-09-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	items, err := store.BuildRetrievalCorpus(ctx, []string{library.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.SourceType == "knowledge" && (item.ReviewStatus != "stable" || strings.Contains(item.Text, "草稿")) {
			t.Fatalf("draft knowledge entered corpus: %+v", item)
		}
	}
}

func TestRetrievalIndexStatePreservesGenerationOnFailure(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, err := store.CreateLibrary(ctx, "Retrieval state", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRetrievalIndexState(ctx, library.ID, "current", "g-old", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRetrievalIndexState(ctx, library.ID, "stale", "", "rebuild failed"); err != nil {
		t.Fatal(err)
	}
	state, err := store.GetRetrievalIndexState(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "stale" || state.Generation != "g-old" || !state.Degraded || state.DegradedReason != "rebuild failed" {
		t.Fatalf("state did not preserve old generation: %+v", state)
	}
}

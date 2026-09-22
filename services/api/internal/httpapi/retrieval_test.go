package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestUnifiedRetrievalUsesAuthoritativeKeywordFallbackAndScope(t *testing.T) {
	server, handler := testServer(t)
	ctx := context.Background()
	first, err := server.Store.CreateLibrary(ctx, "Unified first", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.Store.CreateLibrary(ctx, "Unified second", "")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := server.Store.CreatePendingDocument(ctx, first.ID, "raw.md", "text/markdown", "", "", "", "raw-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Store.ReplaceChunks(ctx, doc.ID, []model.Chunk{{ID: "raw-result", DocumentID: doc.ID, Text: "本地原始检索证据", ContentHash: "raw-hash"}}); err != nil {
		t.Fatal(err)
	}
	other, err := server.Store.CreatePendingDocument(ctx, second.ID, "other.md", "text/markdown", "", "", "", "other-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Store.ReplaceChunks(ctx, other.ID, []model.Chunk{{ID: "other-result", DocumentID: other.ID, Text: "本地原始检索证据", ContentHash: "other-hash"}}); err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/api/v1/retrieval/search", map[string]any{
		"query": "原始检索", "filters": map[string]any{"libraryIds": []string{first.ID}, "sourceType": "raw"}, "topK": 10,
	}, "desktop-test")
	if response.Code != http.StatusOK {
		t.Fatalf("retrieval search: %d %s", response.Code, response.Body.String())
	}
	var result model.RetrievalSearchResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Source != "sqlite-keyword" || !result.KeywordOnly || !result.Degraded || len(result.Results) != 1 || result.Results[0].LibraryID != first.ID {
		t.Fatalf("unexpected fallback result: %+v", result)
	}
	if result.Results[0].SourceType != "raw" || result.Results[0].ReviewStatus != "unreviewed" {
		t.Fatalf("raw citation state was lost: %+v", result.Results[0])
	}
}

func TestUnifiedRetrievalRejectsUnknownLibraryScope(t *testing.T) {
	_, handler := testServer(t)
	response := request(t, handler, http.MethodPost, "/api/v1/retrieval/search", map[string]any{
		"query": "anything", "filters": map[string]any{"libraryIds": []string{"deleted-or-unknown"}},
	}, "desktop-test")
	if response.Code != http.StatusForbidden {
		t.Fatalf("unknown library scope was accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestRetrievalRefreshDuringRunBuildsLatestGeneration(t *testing.T) {
	server, _ := testServer(t)
	library, err := server.Store.CreateLibrary(context.Background(), "queued refresh", "")
	if err != nil {
		t.Fatal(err)
	}
	if !server.beginRetrievalRebuild(library.ID) {
		t.Fatal("failed to start simulated rebuild")
	}
	// A publication arriving after the first corpus read must queue another
	// generation instead of being discarded by the running guard.
	server.refreshUnifiedLibrary(context.Background(), library.ID)

	builds := 0
	generations, err := server.runRetrievalRebuildLoop(context.Background(), library.ID, "", func(ctx context.Context, libraryID, modelID string) (model.RetrievalGeneration, error) {
		builds++
		generation := fmt.Sprintf("generation-%d", builds)
		if err := server.Store.SetRetrievalIndexState(ctx, libraryID, "current", generation, ""); err != nil {
			return model.RetrievalGeneration{}, err
		}
		return model.RetrievalGeneration{LibraryID: libraryID, Generation: generation, Status: "current"}, nil
	}, func() {
		if err := server.Store.SetRetrievalIndexState(context.Background(), library.ID, "queued", "", ""); err != nil {
			t.Fatalf("queue latest generation: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if builds != 2 || len(generations) != 2 {
		t.Fatalf("expected two generations after queued refresh, builds=%d generations=%+v", builds, generations)
	}
	state, err := server.Store.GetRetrievalIndexState(context.Background(), library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "current" || state.Generation != "generation-2" {
		t.Fatalf("latest generation did not become current: %+v", state)
	}
}

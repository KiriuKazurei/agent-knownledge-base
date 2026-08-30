package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/google/uuid"
)

func TestImportedDocumentSummaryReviewAndPublishUsesKAHWorkflow(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	library, err := server.Store.CreateLibrary(ctx, "自动总结全链路", "")
	if err != nil {
		t.Fatal(err)
	}

	summaryHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"schema\":\"kah-knowledge/v1\",\"type\":\"concept\",\"title\":\"自动总结概念\",\"description\":\"从导入资料提炼出的概念。\",\"language\":\"zh-CN\",\"sections\":[{\"id\":\"definition\",\"heading\":\"定义\",\"content\":\"资料说明了该概念的定义。[^source]\"}] }"}}]}`)
	}))
	defer summaryHTTP.Close()
	reviewHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"decision\":\"approve\",\"confidence\":0.99,\"reason\":\"来源和引用完整\",\"issues\":[]}"}}]}`)
	}))
	defer reviewHTTP.Close()
	if err := server.Store.SaveProvider(ctx, model.Provider{ID: "summary-provider", Name: "Summary", Kind: "custom", BaseURL: summaryHTTP.URL, Model: "summary-model", Local: true}); err != nil {
		t.Fatal(err)
	}
	if err := server.Store.SaveProvider(ctx, model.Provider{ID: "review-provider", Name: "Review", Kind: "custom", BaseURL: reviewHTTP.URL, Model: "review-model", Local: true}); err != nil {
		t.Fatal(err)
	}
	automatic := true
	updated, err := server.Store.UpdateLibrary(ctx, library.ID, nil, nil, nil, &automatic, &automatic, stringPtr("summary-provider"), stringPtr("review-provider"))
	if err != nil {
		t.Fatal(err)
	}
	if !updated.AutoSummarizeImports || !updated.AutoReviewAgentSubmissions {
		t.Fatalf("library automation was not configured: %+v", updated)
	}

	document, err := server.Store.CreatePendingDocument(ctx, library.ID, "自动总结资料.md", "text/markdown", "自动总结资料.md", "", "", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Store.ReplaceChunks(ctx, document.ID, []model.Chunk{{ID: uuid.NewString(), DocumentID: document.ID, Text: "这是需要提炼成知识的导入资料。", Location: map[string]any{"ordinal": 0}, ContentHash: "chunk-hash"}}); err != nil {
		t.Fatal(err)
	}

	summaryJob, err := server.Store.CreateJob(ctx, "knowledge_summarize", map[string]any{"documentId": document.ID})
	if err != nil {
		t.Fatal(err)
	}
	server.runKnowledgeSummarize(summaryJob.ID, document.ID)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		jobs, listErr := server.Store.ListJobs(ctx)
		if listErr != nil {
			t.Fatal(listErr)
		}
		var summaryDone bool
		for _, job := range jobs {
			if job.ID == summaryJob.ID {
				if job.Status == "failed" {
					t.Fatalf("summary job failed: %s", job.Message)
				}
				summaryDone = job.Status == "completed"
			}
			if strings.HasPrefix(job.Kind, "kah_knowledge_") && job.Status == "failed" {
				t.Fatalf("KAH workflow job failed: %+v", job)
			}
		}
		items, listErr := server.Store.ListKAHSubmissions(ctx, library.ID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if summaryDone && len(items) == 1 && items[0].ReviewStatus == "published" {
			if len(items[0].Reviews) != 1 || items[0].Reviews[0].ReviewerType != "model" || items[0].Reviews[0].Decision != "approve" {
				t.Fatalf("KAH model review was not recorded: %+v", items[0].Reviews)
			}
			resolved, resolveErr := server.Store.GetKnowledge(ctx, items[0].KnowledgeURI, false)
			if resolveErr != nil {
				t.Fatal(resolveErr)
			}
			if resolved.Status != "stable" || resolved.Payload.Title != "自动总结概念" {
				t.Fatalf("published KAH revision is incomplete: %+v", resolved)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("automatic summary, review, and publish did not complete")
}

func TestNormalizeImportedSummaryRejectsMissingCitation(t *testing.T) {
	detail := model.DocumentDetail{Document: model.Document{ID: uuid.NewString(), Title: "source.md", ContentHash: "hash", Status: "ready"}, Preview: []model.Chunk{{ID: "chunk-1", Text: "source"}}}
	_, err := normalizeImportedSummaryCandidate(`{"schema":"kah-knowledge/v1","type":"concept","title":"概念","description":"描述","language":"zh-CN","sections":[{"id":"definition","heading":"定义","content":"没有来源引用"}]}`, detail)
	if err == nil || !strings.Contains(err.Error(), "missing [^source] citation") {
		t.Fatalf("missing citation error = %v", err)
	}
}

func stringPtr(value string) *string { return &value }

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestKnowledgeSubmissionAutomaticModelApprovalPublishesFormalKnowledge(t *testing.T) {
	t.Skip("superseded by KAH v1 MCP submission and human-review flow")
	server, handler := testServerWithWorker(t)
	libraryID := createStage4Library(t, handler, "模型审核库")
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"decision\":\"approve\",\"confidence\":0.96,\"reason\":\"格式、来源和内容完整\",\"issues\":[]}"}}]}`))
	}))
	defer providerServer.Close()
	providerResponse := request(t, handler, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "本地审核模型", "kind": "lmstudio", "baseUrl": providerServer.URL, "model": "review-model",
	}, "desktop-test")
	if providerResponse.Code != http.StatusOK {
		t.Fatalf("save review provider: %d %s", providerResponse.Code, providerResponse.Body.String())
	}
	var provider model.Provider
	if err := json.Unmarshal(providerResponse.Body.Bytes(), &provider); err != nil {
		t.Fatal(err)
	}
	if !provider.Local {
		t.Fatal("loopback review provider was not recognized as local")
	}
	configured := request(t, handler, http.MethodPatch, "/api/v1/libraries/"+libraryID, map[string]any{
		"autoReviewAgentSubmissions": true, "reviewProviderId": provider.ID,
	}, "desktop-test")
	if configured.Code != http.StatusOK {
		t.Fatalf("configure automatic review: %d %s", configured.Code, configured.Body.String())
	}
	tokenResponse := request(t, handler, http.MethodPost, "/api/v1/tokens", map[string]any{
		"name": "model-review-agent", "scopes": []string{"submit"}, "libraryIds": []string{libraryID},
	}, "desktop-test")
	var token model.AgentToken
	if err := json.Unmarshal(tokenResponse.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	preparation := request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions/prepare?libraryId="+libraryID, nil, token.Secret)
	var prepared model.SubmissionPreparation
	if err := json.Unmarshal(preparation.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	markdown := "---\ntitle: 自动审核中文知识\nsummary: 验证模型批准后才能进入正式索引。\ntags: [自动审核]\nlanguage: zh-CN\nprovenance:\n  type: internal\n  basis: 自动审核全链路测试\n---\n\n# 自动审核中文知识\n\n## 核心内容\n\n模型批准后，中文正文才允许进入正式索引。\n\n## 适用范围\n\n用于自动审核流程验收。\n\n## 限制与不确定性\n\n模型审核仍受置信度阈值约束。\n"
	created := request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions", map[string]any{
		"libraryId": libraryID, "ticket": prepared.Ticket, "clientSubmissionId": "model-review-1", "markdown": markdown,
	}, token.Secret)
	if created.Code != http.StatusCreated {
		t.Fatalf("create automatic review submission: %d %s", created.Code, created.Body.String())
	}
	var submission model.KnowledgeSubmission
	if err := json.Unmarshal(created.Body.Bytes(), &submission); err != nil {
		t.Fatal(err)
	}
	if submission.ReviewJobID == "" {
		t.Fatalf("automatic review job was not created: %#v", submission)
	}
	waitForStage34Job(t, handler, submission.ReviewJobID)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		item, _, err := server.Store.GetKnowledgeSubmission(context.Background(), submission.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if item.ReviewStatus == "published" {
			if item.Status != "ready" || !strings.Contains(item.Markdown, "中文正文才允许") {
				t.Fatalf("published submission is incomplete: %#v", item)
			}
			foundModelApproval := false
			for _, review := range item.Reviews {
				if review.ReviewerType == "model" && review.Decision == "approve" && review.Confidence >= 0.95 {
					foundModelApproval = true
				}
			}
			if !foundModelApproval {
				t.Fatalf("model approval was not recorded: %#v", item.Reviews)
			}
			return
		}
		if item.ReviewStatus == "rejected" || item.ReviewError != "" {
			t.Fatalf("automatic review failed: %#v", item)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("automatic review did not publish submission %s in time", submission.ID)
}

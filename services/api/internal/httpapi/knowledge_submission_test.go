package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestKnowledgeSubmissionChinesePendingReviewAndRevisionLifecycle(t *testing.T) {
	t.Skip("superseded by KAH v1 immutable revision lifecycle")
	server, handler := testServer(t)
	libraryID := createStage4Library(t, handler, "知识提交库")
	tokenResponse := request(t, handler, http.MethodPost, "/api/v1/tokens", map[string]any{
		"name":       "submission-agent",
		"scopes":     []string{"submit"},
		"libraryIds": []string{libraryID},
	}, "desktop-test")
	if tokenResponse.Code != http.StatusCreated {
		t.Fatalf("create submit token: %d %s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var token model.AgentToken
	if err := json.Unmarshal(tokenResponse.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	if token.Secret == "" {
		t.Fatal("submit token secret was not returned once")
	}

	withoutSubmit := request(t, handler, http.MethodGet, "/api/v1/knowledge-submissions", nil, "desktop-test")
	if withoutSubmit.Code != http.StatusOK {
		t.Fatalf("desktop review queue: %d %s", withoutSubmit.Code, withoutSubmit.Body.String())
	}
	withoutSubmit = request(t, handler, http.MethodGet, "/api/v1/knowledge-submissions", nil, "not-a-token")
	if withoutSubmit.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token was accepted: %d %s", withoutSubmit.Code, withoutSubmit.Body.String())
	}

	preparation := request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions/prepare?libraryId="+libraryID, nil, token.Secret)
	if preparation.Code != http.StatusOK {
		t.Fatalf("prepare submission: %d %s", preparation.Code, preparation.Body.String())
	}
	var prepared model.SubmissionPreparation
	if err := json.Unmarshal(preparation.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Ticket == "" || !strings.Contains(prepared.Formatter.Content, "核心内容") {
		t.Fatalf("formatter preparation is incomplete: %#v", prepared)
	}

	markdown := "---\ntitle: 中文知识提交\nsummary: 用于验证中文提交链路不会乱码。\ntags:\n  - 中文\n  - 验收\nlanguage: zh-CN\nprovenance:\n  type: internal\n  basis: 阶段二验收记录\n---\n\n# 中文知识提交\n\n## 核心内容\n\n中文内容应完整保留。\n\n## 适用范围\n\n用于 Agent 知识提交。\n\n## 限制与不确定性\n\n提交仍需人工审核。\n"
	create := request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions", map[string]any{
		"libraryId":          libraryID,
		"ticket":             prepared.Ticket,
		"clientSubmissionId": "中文提交-1",
		"markdown":           markdown,
	}, token.Secret)
	if create.Code != http.StatusCreated {
		t.Fatalf("create submission: %d %s", create.Code, create.Body.String())
	}
	var submission model.KnowledgeSubmission
	if err := json.Unmarshal(create.Body.Bytes(), &submission); err != nil {
		t.Fatal(err)
	}
	if submission.ReviewStatus != "pending_review" || submission.Status != "pending_review" {
		t.Fatalf("submission was not held for review: %#v", submission)
	}

	formal, err := server.Store.Search(context.Background(), model.QueryRequest{Query: "中文知识提交", LibraryIDs: []string{libraryID}, TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(formal) != 0 {
		t.Fatalf("pending submission leaked into formal retrieval: %#v", formal)
	}
	detail := request(t, handler, http.MethodGet, "/api/v1/knowledge-submissions/"+submission.ID, nil, token.Secret)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "中文内容应完整保留") || strings.Contains(detail.Body.String(), string(rune(0xFFFD))) {
		t.Fatalf("Chinese submission detail was corrupted: %d %s", detail.Code, detail.Body.String())
	}
	listed := request(t, handler, http.MethodGet, "/api/v1/knowledge-submissions", nil, token.Secret)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), submission.ID) {
		t.Fatalf("list submission: %d %s", listed.Code, listed.Body.String())
	}

	rejected := request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions/"+submission.ID+"/reject", map[string]any{"reason": "需要补充来源范围"}, "desktop-test")
	if rejected.Code != http.StatusOK || !strings.Contains(rejected.Body.String(), "rejected") {
		t.Fatalf("reject submission: %d %s", rejected.Code, rejected.Body.String())
	}
	revive := request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions/"+submission.ID+"/approve", map[string]any{"reason": "再次确认"}, "desktop-test")
	if revive.Code != http.StatusConflict {
		t.Fatalf("manually rejected submission was revived: %d %s", revive.Code, revive.Body.String())
	}

	preparation = request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions/prepare?libraryId="+libraryID, nil, token.Secret)
	if preparation.Code != http.StatusOK {
		t.Fatalf("prepare revision: %d %s", preparation.Code, preparation.Body.String())
	}
	if err := json.Unmarshal(preparation.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	markdown = strings.Replace(markdown, "阶段二验收记录", "阶段二验收记录，补充人工确认。", 1)
	revision := request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions", map[string]any{
		"libraryId":              libraryID,
		"ticket":                 prepared.Ticket,
		"clientSubmissionId":     "中文提交-2",
		"markdown":               markdown,
		"supersedesSubmissionId": submission.ID,
	}, token.Secret)
	if revision.Code != http.StatusCreated || !strings.Contains(revision.Body.String(), "pending_review") {
		t.Fatalf("create rejected revision: %d %s", revision.Code, revision.Body.String())
	}
}

func TestKnowledgeSubmissionFailedPublicationCanBeRetried(t *testing.T) {
	t.Skip("superseded by KAH v1 immutable draft resubmission workflow")
	server, handler := testServer(t)
	libraryID := createStage4Library(t, handler, "发布重试库")
	tokenResponse := request(t, handler, http.MethodPost, "/api/v1/tokens", map[string]any{
		"name":       "publish-agent",
		"scopes":     []string{"submit"},
		"libraryIds": []string{libraryID},
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
	markdown := "---\ntitle: 发布重试\nsummary: 验证索引不可用时可以恢复。\ntags: [重试]\nlanguage: zh-CN\nprovenance:\n  type: internal\n  basis: 发布任务故障演练\n---\n\n# 发布重试\n\n## 核心内容\n\n发布失败应保留待索引状态。\n\n## 适用范围\n\n用于发布任务恢复。\n\n## 限制与不确定性\n\n需要 Worker 恢复。\n"
	created := request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions", map[string]any{
		"libraryId": libraryID, "ticket": prepared.Ticket, "clientSubmissionId": "publish-retry-1", "markdown": markdown,
	}, token.Secret)
	if created.Code != http.StatusCreated {
		t.Fatalf("create publish submission: %d %s", created.Code, created.Body.String())
	}
	var submission model.KnowledgeSubmission
	if err := json.Unmarshal(created.Body.Bytes(), &submission); err != nil {
		t.Fatal(err)
	}
	approved, err := server.Store.RecordSubmissionReview(context.Background(), submission.ID, "human", "desktop", "approve", 1, "已人工确认", nil)
	if err != nil || !approved {
		t.Fatalf("approve in storage: %v %v", approved, err)
	}
	queued := request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions/"+submission.ID+"/retry-review", nil, "desktop-test")
	if queued.Code != http.StatusAccepted {
		t.Fatalf("queue failed publication retry: %d %s", queued.Code, queued.Body.String())
	}
	var firstJob model.Job
	if err := json.Unmarshal(queued.Body.Bytes(), &firstJob); err != nil {
		t.Fatal(err)
	}
	waitForSubmissionJobFailure(t, server, firstJob.ID)
	item, _, err := server.Store.GetKnowledgeSubmission(context.Background(), submission.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if item.ReviewStatus != "approved_pending_index" || item.Status != "approved_pending_index" || item.ReviewError == "" {
		t.Fatalf("failed publication did not remain retryable: %#v", item)
	}
	second := request(t, handler, http.MethodPost, "/api/v1/knowledge-submissions/"+submission.ID+"/retry-review", nil, "desktop-test")
	if second.Code != http.StatusAccepted {
		t.Fatalf("retry failed publication: %d %s", second.Code, second.Body.String())
	}
	var secondJob model.Job
	if err := json.Unmarshal(second.Body.Bytes(), &secondJob); err != nil {
		t.Fatal(err)
	}
	waitForSubmissionJobFailure(t, server, secondJob.ID)
}

func waitForSubmissionJobFailure(t *testing.T, server *Server, jobID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := server.Store.ListJobs(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.ID == jobID {
				if job.Status == "failed" {
					return
				}
				if job.Status == "cancelled" {
					t.Fatalf("submission job was cancelled: %#v", job)
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("submission job %s did not fail in time", jobID)
}

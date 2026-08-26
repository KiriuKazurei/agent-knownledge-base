package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/config"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/worker"
)

func testServerWithWorker(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	command, args, ok := testWorkerCommand()
	if !ok {
		t.Skip("no usable Python interpreter was found for the API-Worker integration tests")
	}
	store, err := storage.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	_, sourceFile, _, _ := runtime.Caller(0)
	workerRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "worker", "src"))
	workerClient, err := worker.Start(context.Background(), command, args, workerRoot, store.DataRoot)
	if err != nil {
		_ = store.Close()
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(func() {
		_ = workerClient.Close()
		_ = store.Close()
	})
	server := New(config.Config{DesktopToken: "desktop-test", Version: "test"}, store, workerClient, nil)
	return server, server.Router()
}

func testWorkerCommand() (string, []string, bool) {
	if configured := os.Getenv("KAH_TEST_PYTHON"); configured != "" {
		return configured, []string{"-u", "-m", "knowledge_worker"}, true
	}
	if _, err := exec.LookPath("py"); err == nil {
		for _, version := range []string{"3.14", "3.9", "3.7"} {
			if err := exec.Command("py", "-"+version, "-c", "import sys").Run(); err == nil {
				return "py", []string{"-" + version, "-u", "-m", "knowledge_worker"}, true
			}
		}
	}
	if _, err := exec.LookPath("python"); err == nil {
		if err := exec.Command("python", "-c", "import sys").Run(); err == nil {
			return "python", []string{"-u", "-m", "knowledge_worker"}, true
		}
	}
	return "", nil, false
}

func waitForStage34Job(t *testing.T, handler http.Handler, jobID string) model.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response := request(t, handler, http.MethodGet, "/api/v1/jobs", nil, "desktop-test")
		if response.Code != http.StatusOK {
			t.Fatalf("list jobs: %d %s", response.Code, response.Body.String())
		}
		var jobs []model.Job
		if err := json.Unmarshal(response.Body.Bytes(), &jobs); err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.ID != jobID {
				continue
			}
			switch job.Status {
			case "completed":
				return job
			case "failed", "cancelled":
				t.Fatalf("job %s ended as %s: %s", jobID, job.Status, job.Message)
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("job %s did not complete", jobID)
	return model.Job{}
}

func TestStage34FileImportQueryDeduplicationAndEditUseRealWorker(t *testing.T) {
	_, handler := testServerWithWorker(t)
	libraryID := createStage4Library(t, handler, "阶段3-4全链路")
	source := filepath.Join(t.TempDir(), "阶段3-4.md")
	content := "# 阶段三和四\n\n跨链路检索证据必须保留中文和引用位置。\n"
	if err := os.WriteFile(source, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}

	accepted := request(t, handler, http.MethodPost, "/api/v1/imports/files", map[string]any{"libraryId": libraryID, "paths": []string{source}}, "desktop-test")
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("import file: %d %s", accepted.Code, accepted.Body.String())
	}
	var jobs []model.Job
	if err := json.Unmarshal(accepted.Body.Bytes(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected one import job, got %#v", jobs)
	}
	waitForStage34Job(t, handler, jobs[0].ID)

	list := request(t, handler, http.MethodGet, "/api/v1/documents?libraryId="+url.QueryEscape(libraryID), nil, "desktop-test")
	if list.Code != http.StatusOK {
		t.Fatalf("list documents: %d %s", list.Code, list.Body.String())
	}
	var documents []model.Document
	if err := json.Unmarshal(list.Body.Bytes(), &documents); err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 || documents[0].Status != "ready" {
		t.Fatalf("imported document is not ready: %#v", documents)
	}
	documentID := documents[0].ID
	detailResponse := request(t, handler, http.MethodGet, "/api/v1/documents/"+documentID, nil, "desktop-test")
	var detail model.DocumentDetail
	if detailResponse.Code != http.StatusOK || json.Unmarshal(detailResponse.Body.Bytes(), &detail) != nil {
		t.Fatalf("document detail: %d %s", detailResponse.Code, detailResponse.Body.String())
	}
	if len(detail.Preview) == 0 || !strings.Contains(detail.Preview[0].Text, "跨链路检索证据") || detail.Preview[0].Location["heading"] != "阶段三和四" {
		t.Fatalf("worker preview lost text or location: %#v", detail.Preview)
	}

	query := request(t, handler, http.MethodPost, "/api/v1/query", map[string]any{"query": "跨链路检索", "libraryIds": []string{libraryID}, "retrievalMode": "hybrid", "topK": 10}, "desktop-test")
	if query.Code != http.StatusOK {
		t.Fatalf("hybrid query: %d %s", query.Code, query.Body.String())
	}
	var result model.QueryResponse
	if err := json.Unmarshal(query.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Degraded || len(result.Evidence) != 1 || result.Evidence[0].DocumentID != documentID || result.Evidence[0].Scores.Final <= 0 || result.Evidence[0].Scores.Fusion <= 0 {
		t.Fatalf("query did not use the live worker and hydrated citation: %+v", result)
	}

	duplicate := request(t, handler, http.MethodPost, "/api/v1/imports/files", map[string]any{"libraryId": libraryID, "paths": []string{source}}, "desktop-test")
	if duplicate.Code != http.StatusAccepted {
		t.Fatalf("duplicate import: %d %s", duplicate.Code, duplicate.Body.String())
	}
	var duplicateJobs []model.Job
	if err := json.Unmarshal(duplicate.Body.Bytes(), &duplicateJobs); err != nil {
		t.Fatal(err)
	}
	completedDuplicate := waitForStage34Job(t, handler, duplicateJobs[0].ID)
	if !strings.Contains(completedDuplicate.Message, "Deduplicated") {
		t.Fatalf("duplicate import was not reported as deduplicated: %#v", completedDuplicate)
	}

	updatedContent := "# 已编辑内容\n\n新的阶段四答案证据仍然需要被检索。\n"
	updated := request(t, handler, http.MethodPatch, "/api/v1/documents/"+documentID, map[string]any{"content": updatedContent, "tags": []string{"阶段4"}, "favorite": true}, "desktop-test")
	if updated.Code != http.StatusOK || !bytes.Contains(updated.Body.Bytes(), []byte(`"status":"ready"`)) {
		t.Fatalf("update document: %d %s", updated.Code, updated.Body.String())
	}
	queryUpdated := request(t, handler, http.MethodPost, "/api/v1/query", map[string]any{"query": "新的阶段四答案", "libraryIds": []string{libraryID}, "retrievalMode": "lexical"}, "desktop-test")
	if queryUpdated.Code != http.StatusOK || !strings.Contains(queryUpdated.Body.String(), "新的阶段四答案证据") {
		t.Fatalf("edited content was not re-indexed: %d %s", queryUpdated.Code, queryUpdated.Body.String())
	}
}

func TestStage3SourceWatchAndFolderFilterUseRealWorker(t *testing.T) {
	_, handler := testServerWithWorker(t)
	libraryID := createStage4Library(t, handler, "来源监视")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(root, "root.md"):             "# Root\n\nroot source\n",
		filepath.Join(root, "nested", "child.txt"): "child source\n",
		filepath.Join(root, "ignored.bin"):         "not imported\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	createdWatch := request(t, handler, http.MethodPost, "/api/v1/sources/watches", map[string]any{"libraryId": libraryID, "rootPath": root, "recursive": true}, "desktop-test")
	if createdWatch.Code != http.StatusAccepted {
		t.Fatalf("create source watch: %d %s", createdWatch.Code, createdWatch.Body.String())
	}
	var watchResult struct {
		Watch model.SourceWatch `json:"watch"`
		Job   model.Job         `json:"job"`
	}
	if err := json.Unmarshal(createdWatch.Body.Bytes(), &watchResult); err != nil {
		t.Fatal(err)
	}
	if watchResult.Watch.ID == "" || watchResult.Job.ID == "" {
		t.Fatalf("source watch response is incomplete: %#v", watchResult)
	}
	completed := waitForStage34Job(t, handler, watchResult.Job.ID)
	if completed.Message != "Scanned 2 files" {
		t.Fatalf("source watch scanned an unexpected set: %#v", completed)
	}

	documentsResponse := request(t, handler, http.MethodGet, "/api/v1/documents?libraryId="+url.QueryEscape(libraryID), nil, "desktop-test")
	var documents []model.Document
	if documentsResponse.Code != http.StatusOK || json.Unmarshal(documentsResponse.Body.Bytes(), &documents) != nil {
		t.Fatalf("list watched documents: %d %s", documentsResponse.Code, documentsResponse.Body.String())
	}
	if len(documents) != 2 {
		t.Fatalf("expected two recursively imported documents, got %#v", documents)
	}

	folderResponse := request(t, handler, http.MethodPost, "/api/v1/folders", map[string]any{"libraryId": libraryID, "name": "重点资料"}, "desktop-test")
	if folderResponse.Code != http.StatusCreated {
		t.Fatalf("create folder: %d %s", folderResponse.Code, folderResponse.Body.String())
	}
	var folder model.VirtualFolder
	if err := json.Unmarshal(folderResponse.Body.Bytes(), &folder); err != nil {
		t.Fatal(err)
	}
	favorite := true
	if response := request(t, handler, http.MethodPatch, "/api/v1/documents/"+documents[0].ID, map[string]any{"favorite": favorite}, "desktop-test"); response.Code != http.StatusOK {
		t.Fatalf("favorite document: %d %s", response.Code, response.Body.String())
	}
	if response := request(t, handler, http.MethodPut, "/api/v1/documents/"+documents[0].ID+"/folders/"+folder.ID, nil, "desktop-test"); response.Code != http.StatusNoContent {
		t.Fatalf("assign folder: %d %s", response.Code, response.Body.String())
	}
	filtered := request(t, handler, http.MethodGet, "/api/v1/documents?libraryId="+url.QueryEscape(libraryID)+"&folderId="+url.QueryEscape(folder.ID)+"&favorite=true", nil, "desktop-test")
	var filteredDocuments []model.Document
	if filtered.Code != http.StatusOK || json.Unmarshal(filtered.Body.Bytes(), &filteredDocuments) != nil {
		t.Fatalf("filter folder favorites: %d %s", filtered.Code, filtered.Body.String())
	}
	if len(filteredDocuments) != 1 || filteredDocuments[0].ID != documents[0].ID || !filteredDocuments[0].Favorite {
		t.Fatalf("folder/favorite filter returned the wrong documents: %#v", filteredDocuments)
	}
}

func TestStage4ProviderModelsAnswerStreamAndAgentQueryUseRealWorker(t *testing.T) {
	server, handler := testServerWithWorker(t)
	libraryID := createStage4Library(t, handler, "回答与 Agent")
	doc, err := server.Store.CreatePendingDocument(context.Background(), libraryID, "answer.md", "text/markdown", "answer.md", "", "objects/answer", "answer-hash")
	if err != nil {
		t.Fatal(err)
	}
	chunk := model.Chunk{ID: "answer-chunk", DocumentID: doc.ID, Text: "可引用答案的事实证据", Location: map[string]any{"heading": "答案"}, ContentHash: "answer-hash"}
	if err := server.Store.ReplaceChunks(context.Background(), doc.ID, []model.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	if err := server.Worker.Call(context.Background(), "index_upsert", map[string]any{"libraryId": libraryID, "documentId": doc.ID, "chunks": []model.Chunk{chunk}}, nil); err != nil {
		t.Fatal(err)
	}

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"stage4-model"}]}`))
		case "/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte("可引用答案的事实证据")) {
				http.Error(w, "evidence was not sent to provider", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"基于证据的回答 [1]"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()

	providerResponse := request(t, handler, http.MethodPost, "/api/v1/providers", map[string]any{"name": "Stage4 provider", "kind": "custom", "baseUrl": providerServer.URL, "model": "stage4-model"}, "desktop-test")
	if providerResponse.Code != http.StatusOK {
		t.Fatalf("save provider: %d %s", providerResponse.Code, providerResponse.Body.String())
	}
	var provider model.Provider
	if err := json.Unmarshal(providerResponse.Body.Bytes(), &provider); err != nil {
		t.Fatal(err)
	}
	models := request(t, handler, http.MethodGet, "/api/v1/providers/"+provider.ID+"/models", nil, "desktop-test")
	if models.Code != http.StatusOK || !strings.Contains(models.Body.String(), "stage4-model") {
		t.Fatalf("provider models: %d %s", models.Code, models.Body.String())
	}
	if allowed := request(t, handler, http.MethodPatch, "/api/v1/libraries/"+libraryID, map[string]any{"allowRemoteModels": true}, "desktop-test"); allowed.Code != http.StatusOK {
		t.Fatalf("allow answer provider: %d %s", allowed.Code, allowed.Body.String())
	}

	answer := request(t, handler, http.MethodPost, "/api/v1/query", map[string]any{"query": "可引用答案", "libraryIds": []string{libraryID}, "responseMode": "answer", "providerId": provider.ID}, "desktop-test")
	if answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), "基于证据的回答 [1]") {
		t.Fatalf("answer query: %d %s", answer.Code, answer.Body.String())
	}
	stream := request(t, handler, http.MethodPost, "/api/v1/query/stream", map[string]any{"query": "可引用答案", "libraryIds": []string{libraryID}, "responseMode": "answer", "providerId": provider.ID}, "desktop-test")
	streamBody := stream.Body.String()
	if stream.Code != http.StatusOK || !strings.Contains(streamBody, "event: retrieval") || !strings.Contains(streamBody, "event: citation") || !strings.Contains(streamBody, "event: answer_delta") || !strings.Contains(streamBody, "基于证据的回答 [1]") || !strings.Contains(streamBody, "event: complete") {
		t.Fatalf("answer stream: %d %s", stream.Code, streamBody)
	}

	tokenResponse := request(t, handler, http.MethodPost, "/api/v1/tokens", map[string]any{"name": "stage4-query-agent", "scopes": []string{"query"}, "libraryIds": []string{libraryID}}, "desktop-test")
	if tokenResponse.Code != http.StatusCreated {
		t.Fatalf("create query token: %d %s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var token model.AgentToken
	if err := json.Unmarshal(tokenResponse.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	if token.Secret == "" {
		t.Fatal("token secret was not returned at creation")
	}
	agentQuery := request(t, handler, http.MethodPost, "/api/v1/query", map[string]any{"query": "可引用答案", "libraryIds": []string{libraryID}}, token.Secret)
	if agentQuery.Code != http.StatusOK || !strings.Contains(agentQuery.Body.String(), "answer-chunk") {
		t.Fatalf("agent query: %d %s", agentQuery.Code, agentQuery.Body.String())
	}
	if management := request(t, handler, http.MethodGet, "/api/v1/libraries", nil, token.Secret); management.Code != http.StatusForbidden {
		t.Fatalf("agent token gained management access: %d %s", management.Code, management.Body.String())
	}
}

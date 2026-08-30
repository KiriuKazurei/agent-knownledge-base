package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/config"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/worker"
	"github.com/google/uuid"
)

func requiredImportTestServerWithWorker(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	command, args, ok := testWorkerCommand()
	if !ok {
		t.Fatal("a usable Python worker is required for the import/index gate")
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

func TestFileImportCreatesTraceablePendingKAHDraft(t *testing.T) {
	_, handler := requiredImportTestServerWithWorker(t)
	libraryID := createStage4Library(t, handler, "导入资料门禁")
	source := filepath.Join(t.TempDir(), "imported-guide.md")
	content := "# 导入指南\n\n这段资料必须在导入后保留，并且草稿正文必须能够回指原始文档。\n"
	if err := os.WriteFile(source, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}

	accepted := request(t, handler, http.MethodPost, "/api/v1/imports/files", map[string]any{
		"libraryId": libraryID,
		"paths":     []string{source},
	}, "desktop-test")
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
	job := waitForStage34Job(t, handler, jobs[0].ID)
	if job.Status != "completed" {
		t.Fatalf("import job did not complete: %+v", job)
	}
	if !strings.Contains(job.Message, "KAH draft") || !strings.Contains(job.Message, "awaiting review") {
		t.Fatalf("completed import did not report a pending KAH draft: %+v", job)
	}

	documentsResponse := request(t, handler, http.MethodGet, "/api/v1/documents?libraryId="+libraryID, nil, "desktop-test")
	if documentsResponse.Code != http.StatusOK {
		t.Fatalf("list documents: %d %s", documentsResponse.Code, documentsResponse.Body.String())
	}
	var documents []model.Document
	if err := json.Unmarshal(documentsResponse.Body.Bytes(), &documents); err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 || documents[0].Status != "ready" || documents[0].ContentHash == "" {
		t.Fatalf("imported document is not ready or has no snapshot hash: %#v", documents)
	}
	document := documents[0]
	detailResponse := request(t, handler, http.MethodGet, "/api/v1/documents/"+document.ID, nil, "desktop-test")
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("get document detail: %d %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail model.DocumentDetail
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Preview) == 0 || !strings.Contains(detail.Preview[0].Text, "这段资料必须在导入后保留") {
		t.Fatalf("document parsing did not preserve source text: %#v", detail.Preview)
	}

	submissionsResponse := request(t, handler, http.MethodGet, "/api/v1/knowledge/submissions?libraryId="+libraryID, nil, "desktop-test")
	if submissionsResponse.Code != http.StatusOK {
		t.Fatalf("list KAH submissions: %d %s", submissionsResponse.Code, submissionsResponse.Body.String())
	}
	var submissions []model.KAHSubmission
	if err := json.Unmarshal(submissionsResponse.Body.Bytes(), &submissions); err != nil {
		t.Fatal(err)
	}
	if len(submissions) != 1 {
		t.Fatalf("expected one imported KAH submission, got %#v", submissions)
	}
	submission := submissions[0]
	if submission.ReviewStatus != "pending_review" {
		t.Fatalf("imported submission is not pending_review: %+v", submission)
	}
	if len(submission.Validation.Normalized.Sources) != 1 {
		t.Fatalf("imported submission has unexpected sources: %#v", submission.Validation.Normalized.Sources)
	}
	sourceSnapshot := submission.Validation.Normalized.Sources[0]
	if sourceSnapshot.Resource != storage.DocumentURI(document.ID) {
		t.Fatalf("submission source = %q, want %q", sourceSnapshot.Resource, storage.DocumentURI(document.ID))
	}
	if sourceSnapshot.Snapshot.ContentHash != document.ContentHash {
		t.Fatalf("submission content hash = %q, want %q", sourceSnapshot.Snapshot.ContentHash, document.ContentHash)
	}
	if !strings.Contains(submission.Validation.Normalized.Sections[0].Content, "[^source]") {
		t.Fatalf("normalized draft body lost the source citation: %#v", submission.Validation.Normalized.Sections)
	}
	if !strings.Contains(submission.Markdown, "[^source]") || !strings.Contains(submission.Markdown, storage.DocumentURI(document.ID)) {
		t.Fatalf("rendered draft body lost the source reference: %q", submission.Markdown)
	}
	if strings.Contains(submission.Markdown, "AI summary") || strings.Contains(submission.Markdown, "语义总结") {
		t.Fatalf("imported reference draft made an unsupported AI-summary claim: %q", submission.Markdown)
	}

}

func TestDuplicateFileImportDoesNotCreateSecondKAHDraft(t *testing.T) {
	_, handler := requiredImportTestServerWithWorker(t)
	libraryID := createStage4Library(t, handler, "导入幂等门禁")
	source := filepath.Join(t.TempDir(), "duplicate.md")
	if err := os.WriteFile(source, []byte("# 幂等资料\n\n同一来源重复导入只能保留一个知识草稿。\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		accepted := request(t, handler, http.MethodPost, "/api/v1/imports/files", map[string]any{
			"libraryId": libraryID,
			"paths":     []string{source},
		}, "desktop-test")
		if accepted.Code != http.StatusAccepted {
			t.Fatalf("import attempt %d: %d %s", attempt+1, accepted.Code, accepted.Body.String())
		}
		var jobs []model.Job
		if err := json.Unmarshal(accepted.Body.Bytes(), &jobs); err != nil {
			t.Fatal(err)
		}
		if len(jobs) != 1 {
			t.Fatalf("attempt %d returned unexpected jobs: %#v", attempt+1, jobs)
		}
		completed := waitForStage34Job(t, handler, jobs[0].ID)
		if attempt == 1 && !strings.Contains(completed.Message, "Deduplicated") {
			t.Fatalf("duplicate import was not reported as deduplicated: %+v", completed)
		}
	}

	submissionsResponse := request(t, handler, http.MethodGet, "/api/v1/knowledge/submissions?libraryId="+libraryID, nil, "desktop-test")
	if submissionsResponse.Code != http.StatusOK {
		t.Fatalf("list KAH submissions: %d %s", submissionsResponse.Code, submissionsResponse.Body.String())
	}
	var submissions []model.KAHSubmission
	if err := json.Unmarshal(submissionsResponse.Body.Bytes(), &submissions); err != nil {
		t.Fatal(err)
	}
	if len(submissions) != 1 {
		t.Fatalf("duplicate import created %d KAH drafts: %#v", len(submissions), submissions)
	}
}

func TestStorageRejectsInvalidImportedDocumentSources(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	localLibrary, err := server.Store.CreateLibrary(ctx, "本地资料库", "")
	if err != nil {
		t.Fatal(err)
	}
	foreignLibrary, err := server.Store.CreateLibrary(ctx, "其他资料库", "")
	if err != nil {
		t.Fatal(err)
	}
	localDocument := createReadyImportDocument(t, server, localLibrary.ID, "local.md", "local-hash")
	foreignDocument := createReadyImportDocument(t, server, foreignLibrary.ID, "foreign.md", "foreign-hash")

	cases := []struct {
		name       string
		libraryID  string
		resourceID string
		hash       string
		wantError  string
	}{
		{name: "missing document", libraryID: localLibrary.ID, resourceID: uuid.NewString(), hash: "missing-hash", wantError: "was not found"},
		{name: "cross library document", libraryID: localLibrary.ID, resourceID: foreignDocument.ID, hash: foreignDocument.ContentHash, wantError: "another library"},
		{name: "wrong content hash", libraryID: localLibrary.ID, resourceID: localDocument.ID, hash: "wrong-hash", wantError: "does not match"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := server.Store.CreateKnowledgeDraft(ctx, storage.KnowledgeDraftInput{
				LibraryID:          testCase.libraryID,
				ClientSubmissionID: "invalid-document-source-" + testCase.name,
				Mode:               "create",
				Payload:            importedDocumentSourcePayload(testCase.resourceID, testCase.hash),
				RequireSources:     true,
			})
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("invalid document source error = %v, want substring %q", err, testCase.wantError)
			}
		})
	}
}

func createReadyImportDocument(t *testing.T, server *Server, libraryID, title, hash string) model.Document {
	t.Helper()
	document, err := server.Store.CreatePendingDocument(context.Background(), libraryID, title, "text/markdown", "", "", "", hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Store.ReplaceChunks(context.Background(), document.ID, []model.Chunk{{
		ID: "chunk-" + document.ID, DocumentID: document.ID, Text: "可引用的来源正文。", Location: map[string]any{"ordinal": 0}, ContentHash: hash,
	}}); err != nil {
		t.Fatal(err)
	}
	return document
}

func importedDocumentSourcePayload(documentID, contentHash string) model.KnowledgePayload {
	return model.KnowledgePayload{
		Schema: "kah-knowledge/v1", Type: "claim", Subtype: "test:document-source",
		Title: "文档来源验证", Description: "验证导入文档来源必须存在且快照一致。", Language: "zh-CN",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "主张", Content: "此正文引用导入文档来源。[^source]"}},
		Sources: []model.KnowledgeSource{{
			ID: "source", Resource: storage.DocumentURI(documentID), Title: "导入文档",
			Snapshot: model.KnowledgeSourceSnapshot{Status: "captured", ContentHash: contentHash},
		}},
	}
}

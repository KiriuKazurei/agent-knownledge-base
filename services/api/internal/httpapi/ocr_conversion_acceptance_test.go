package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/worker"
)

// Acceptance regression: an expired work context must not prevent recording
// the terminal failure, otherwise the desktop continues to show a running job.
func TestAcceptanceExpiredSummaryContextPersistsFailure(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	job, err := server.Store.CreateJob(ctx, "knowledge_summarize", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Store.UpdateJob(ctx, job.ID, "running", .3, "working"); err != nil {
		t.Fatal(err)
	}
	expired, cancel := context.WithCancel(ctx)
	cancel()
	server.failJob(expired, job.ID, context.DeadlineExceeded)
	jobs, err := server.Store.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if jobs[0].Status != "failed" {
		t.Fatalf("expired task remained %q instead of failed", jobs[0].Status)
	}
}

// Acceptance regression: recovered work must not create a reference draft
// merely because the current policy is off.
func TestAcceptanceRecoveredSummaryHonorsOffMode(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	library, err := server.Store.CreateLibrary(ctx, "acceptance", "")
	if err != nil {
		t.Fatal(err)
	}
	off := "off"
	if _, err := server.Store.UpdateLibraryPatch(ctx, library.ID, storage.LibraryPatch{ImportConversionMode: &off}); err != nil {
		t.Fatal(err)
	}
	doc, err := server.Store.CreatePendingDocument(ctx, library.ID, "source.md", "text/markdown", "source.md", "", "", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Store.ReplaceChunks(ctx, doc.ID, []model.Chunk{{ID: "acceptance-chunk", DocumentID: doc.ID, Text: "source evidence", ContentHash: "hash"}}); err != nil {
		t.Fatal(err)
	}
	job, err := server.Store.CreateJob(ctx, "knowledge_summarize", map[string]any{"documentId": doc.ID})
	if err != nil {
		t.Fatal(err)
	}
	server.runKnowledgeSummarize(job.ID, doc.ID)
	submissions, err := server.Store.ListKAHSubmissions(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(submissions) != 0 {
		t.Fatalf("off mode created %d KAH submission(s) on replay", len(submissions))
	}
}

func TestAcceptanceCheckpointRejectsChangedModelConfiguration(t *testing.T) {
	run := importedSummaryRun{Provider: model.Provider{ID: "same", Model: "old"}, SourceHash: "source"}
	saved := importedSummaryCheckpoint{Version: 2, RunHash: importedRunHash(run), NextBatch: 1, Candidates: []model.KnowledgePayload{{Title: "old"}}}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	run.Provider.Model = "new"
	expected := saved
	expected.RunHash = importedRunHash(run)
	expected.NextBatch = 0
	expected.Candidates = nil
	_, next := restoreImportedSummaryCheckpoint(data, expected, 2)
	if next != 0 {
		t.Fatal("reused candidates from a different model")
	}
}

func TestAcceptanceReviewQueueRepairsCrashGapWithoutDuplicateJobs(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	id, created, err := server.Store.EnsureReviewJob(ctx, "persisted-submission")
	if err != nil || !created {
		t.Fatalf("repair failed: %v", err)
	}
	again, created, err := server.Store.EnsureReviewJob(ctx, "persisted-submission")
	if err != nil || created || again != id {
		t.Fatalf("duplicate review dispatch: %v", err)
	}
	if err := server.Store.UpdateJob(ctx, id, "completed", 1, "reviewed"); err != nil {
		t.Fatal(err)
	}
	_, created, err = server.Store.EnsureReviewJob(ctx, "persisted-submission")
	if err != nil || created {
		t.Fatalf("completed review dispatched again: %v", err)
	}
}

func TestAcceptanceBatchSeparatorsDoNotTruncateTail(t *testing.T) {
	chunks := []model.Chunk{{Text: strings.Repeat("a", 100)}, {Text: strings.Repeat("b", 100) + "TAIL"}}
	var joined strings.Builder
	for _, batch := range importedSummaryBatches(chunks, 200) {
		joined.WriteString(importedReferenceExtract(batch, 200))
	}
	if !strings.HasSuffix(joined.String(), "TAIL") || strings.Count(joined.String(), "b") != 100 {
		t.Fatal("batch separator budget truncated source")
	}
}

func TestAcceptanceLegacyOverlapIsRemovedOnlyWithinSameBlock(t *testing.T) {
	chunks := []model.Chunk{
		{Text: "abcdef", Location: map[string]any{"page": 1, "charStart": 0, "charEnd": 6}},
		{Text: "efghij", Location: map[string]any{"page": 1, "charStart": 4, "charEnd": 10}},
		{Text: "efghij", Location: map[string]any{"page": 2, "charStart": 4, "charEnd": 10}},
	}
	actual := importedNonOverlappingChunks(chunks)
	if actual[1].Text != "ghij" || actual[2].Text != "efghij" {
		t.Fatalf("incorrect overlap handling: %+v", actual)
	}
}

func TestAcceptanceWorkerQualityAndErrorsReachAPI(t *testing.T) {
	command, args, ok := testWorkerCommand()
	if !ok {
		t.Skip("Python unavailable")
	}
	// Real RPC pipes, controlled parser responses: not a real OCR-engine test.
	script := `import json,sys
for line in sys.stdin:
 request=json.loads(line)
 response={"jsonrpc":"2.0","id":request["id"]}
 if request["method"]=="health": response["result"]={"status":"ok"}
 elif request["params"]["path"]=="incomplete": response["result"]={"extraction":{"status":"incomplete","missingPages":[2]},"chunks":[{"id":"partial","text":"partial text"}]}
 else: response["error"]={"code":-32000,"message":"offline OCR models are not installed"}
 print(json.dumps(response),flush=True)
`
	server, _ := testServer(t)
	args = append(args[:len(args)-2], "-c", script)
	client, err := worker.Start(context.Background(), command, args, t.TempDir(), server.Store.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server.Worker = client
	chunks, err := server.parseDocument(context.Background(), model.Document{ID: "pdf", MediaType: "application/pdf"}, "incomplete")
	if err == nil || len(chunks) != 0 || jobErrorCode(err.Error()) != "ocr_extraction_incomplete" {
		t.Fatalf("partial source escaped gate: %v %v", chunks, err)
	}
	_, err = server.parseDocument(context.Background(), model.Document{ID: "pdf", MediaType: "application/pdf"}, "models")
	if err == nil || !strings.Contains(err.Error(), "models are not installed") || jobErrorCode(err.Error()) != "ocr_model_missing" {
		t.Fatalf("lost model error: %v", err)
	}
}

func TestAcceptanceReviewQueueRecognizesLegacyJob(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	if _, err := server.Store.CreateJob(ctx, "kah_knowledge_review", map[string]string{"submissionId": "legacy"}); err != nil {
		t.Fatal(err)
	}
	_, created, err := server.Store.EnsureReviewJob(ctx, "legacy")
	if err != nil || created {
		t.Fatalf("duplicated legacy review: %v", err)
	}
}

func TestAcceptancePinnedModeSurvivesLibrarySettingChange(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	library, err := server.Store.CreateLibrary(ctx, "snapshot", "")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := server.Store.CreatePendingDocument(ctx, library.ID, "source.md", "text/markdown", "source.md", "", "", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Store.ReplaceChunks(ctx, doc.ID, []model.Chunk{{ID: "snapshot-chunk", DocumentID: doc.ID, Text: "source evidence", ContentHash: "chunk-hash"}}); err != nil {
		t.Fatal(err)
	}
	chunks, err := server.loadAllDocumentChunks(ctx, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	run := importedSummaryRun{Library: library, SourceHash: importedRunHash(chunks)}
	job, err := server.Store.CreateJob(ctx, "knowledge_summarize", map[string]any{"documentId": doc.ID, "contentHash": "hash", "run": run})
	if err != nil {
		t.Fatal(err)
	}
	off := "off"
	if _, err := server.Store.UpdateLibraryPatch(ctx, library.ID, storage.LibraryPatch{ImportConversionMode: &off}); err != nil {
		t.Fatal(err)
	}
	server.runKnowledgeSummarize(job.ID, doc.ID)
	submissions, err := server.Store.ListKAHSubmissions(ctx, library.ID)
	if err != nil || len(submissions) != 1 {
		t.Fatalf("lost captured reference policy: %v %v", submissions, err)
	}
}

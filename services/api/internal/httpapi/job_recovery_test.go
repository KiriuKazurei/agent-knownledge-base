package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestQueuedJobRecoveryMatrixResumesAllStageTwoJobs(t *testing.T) {
	command, args, ok := testWorkerCommand()
	if !ok {
		t.Skip("no usable Python interpreter was found for the recovery matrix")
	}
	ctx := context.Background()
	dataRoot := t.TempDir()
	sourceRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "watch-note.md")
	if err := os.WriteFile(sourcePath, []byte("# Watch recovery\n\nSource watch jobs survive restart."), 0o640); err != nil {
		t.Fatal(err)
	}
	skillRoot := t.TempDir()
	skillPath := filepath.Join(skillRoot, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: matrix-skill\ndescription: Recovery matrix validation skill\n---\n\n# Matrix skill\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/robots.txt":
			response.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(response, "User-agent: *\nAllow: /\n")
		case "/page.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, "<html><body><h1>URL recovery</h1><p>Persisted URL import.</p></body></html>")
		default:
			http.NotFound(response, request)
		}
	}))
	defer web.Close()

	first, err := storage.Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	library, err := first.CreateLibrary(ctx, "Recovery matrix", "")
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	watch, err := first.CreateSourceWatch(ctx, library.ID, sourceRoot, true)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	jobs := make(map[string]model.Job)
	create := func(kind string, payload map[string]any) {
		job, createErr := first.CreateJob(ctx, kind, payload)
		if createErr != nil {
			_ = first.Close()
			t.Fatalf("create %s job: %v", kind, createErr)
		}
		if updateErr := first.UpdateJob(ctx, job.ID, "running", 0.35, "Simulated process interruption"); updateErr != nil {
			_ = first.Close()
			t.Fatalf("mark %s job running: %v", kind, updateErr)
		}
		jobs[kind] = job
	}
	create("url_import", map[string]any{"libraryId": library.ID, "url": web.URL + "/page.html", "maxDepth": 0, "maxPages": 1})
	create("skill_import", map[string]any{"path": skillPath, "replace": false})
	create("source_scan", map[string]any{"watchId": watch.ID})
	create("index_rebuild", map[string]any{"libraryId": library.ID})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := storage.Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_, sourceFile, _, _ := runtime.Caller(0)
	workerRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "worker", "src"))
	workerClient, err := worker.Start(ctx, command, args, workerRoot, second.DataRoot)
	if err != nil {
		t.Fatalf("start recovery worker: %v", err)
	}
	defer workerClient.Close()
	server := New(config.Config{DesktopToken: "desktop-test", Version: "test"}, second, workerClient, nil)
	if err := server.ResumeQueuedJobs(ctx); err != nil {
		t.Fatal(err)
	}

	completed := waitForRecoveredJobs(t, second, jobs)
	for kind, job := range completed {
		if job.Status != "completed" {
			t.Fatalf("%s recovery status: %+v", kind, job)
		}
	}
	documents, err := second.ListDocuments(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) < 2 {
		t.Fatalf("expected URL and source-watch documents, got %d: %+v", len(documents), documents)
	}
	for _, document := range documents {
		if document.Status != "ready" {
			t.Fatalf("recovered document is not ready: %+v", document)
		}
	}
	skills, err := second.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundSkill := false
	for _, skill := range skills {
		if skill.Name == "matrix-skill" && skill.Status == "ready" {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Fatalf("recovered Skill not found: %+v", skills)
	}
	recoveredWatch, err := second.GetSourceWatch(ctx, watch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recoveredWatch.LastMessage, "Scanned 1 files") {
		t.Fatalf("source watch scan was not persisted: %+v", recoveredWatch)
	}
}

func TestQueuedJobRecoveryRejectsInvalidPayloads(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	malformed, err := store.CreateJob(ctx, "file_import", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, "UPDATE jobs SET payload_json=? WHERE id=?", "{", malformed.ID); err != nil {
		t.Fatal(err)
	}
	unknown, err := store.CreateJob(ctx, "future_job", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	server := New(config.Config{DesktopToken: "desktop-test", Version: "test"}, store, nil, nil)
	if err := server.ResumeQueuedJobs(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]model.Job{}
	for _, job := range jobs {
		statuses[job.ID] = job
	}
	for _, job := range []model.Job{malformed, unknown} {
		actual, ok := statuses[job.ID]
		if !ok || actual.Status != "failed" {
			t.Fatalf("invalid job was not failed: %+v", actual)
		}
		if actual.Message == "" {
			t.Fatalf("invalid job has no diagnostic: %+v", actual)
		}
	}
}

func waitForRecoveredJobs(t *testing.T, store *storage.Store, expected map[string]model.Job) map[string]model.Job {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := store.ListJobs(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		found := make(map[string]model.Job, len(expected))
		for _, job := range jobs {
			for _, target := range expected {
				if job.ID == target.ID {
					found[target.Kind] = job
				}
			}
		}
		allDone := len(found) == len(expected)
		for kind, job := range found {
			if job.Status == "failed" {
				t.Fatalf("recovered %s job failed: %s", kind, job.Message)
			}
			if job.Status != "completed" {
				allDone = false
			}
		}
		if allDone {
			return found
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("recovery matrix did not complete")
	return nil
}

func TestQueuedFileImportResumesAfterRestart(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	sourceRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "restart-note.md")
	if err := os.WriteFile(sourcePath, []byte("# Restart proof\n\nThis document survives an API restart."), 0o640); err != nil {
		t.Fatal(err)
	}

	first, err := storage.Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	library, err := first.CreateLibrary(ctx, "Recovery", "")
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	job, err := first.CreateJob(ctx, "file_import", map[string]any{"libraryId": library.ID, "path": sourcePath})
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	if err := first.UpdateJob(ctx, job.ID, "running", 0.4, "Simulated process interruption"); err != nil {
		first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := storage.Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	server := New(config.Config{DesktopToken: "desktop-test", Version: "test"}, second, nil, nil)
	if err := server.ResumeQueuedJobs(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		jobs, listErr := second.ListJobs(ctx)
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, item := range jobs {
			if item.ID != job.ID {
				continue
			}
			if item.Status == "failed" {
				t.Fatalf("recovered import failed: %s", item.Message)
			}
			if item.Status == "completed" {
				documents, docErr := second.ListDocuments(ctx, library.ID)
				if docErr != nil {
					t.Fatal(docErr)
				}
				if len(documents) != 1 || documents[0].Status != "ready" {
					t.Fatalf("recovered documents: %+v", documents)
				}
				detail, detailErr := second.GetDocument(ctx, documents[0].ID)
				if detailErr != nil {
					t.Fatal(detailErr)
				}
				if len(detail.Preview) == 0 {
					t.Fatal("recovered document has no chunks")
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("recovered import did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

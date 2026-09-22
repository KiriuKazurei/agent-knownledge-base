package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestArchitectureProjectDesktopLifecycleAndScan(t *testing.T) {
	_, handler := testServer(t)
	libraryResponse := request(t, handler, http.MethodPost, "/api/v1/libraries", map[string]any{"name": "Architecture"}, "desktop-test")
	if libraryResponse.Code != http.StatusCreated {
		t.Fatalf("library: %d %s", libraryResponse.Code, libraryResponse.Body.String())
	}
	var library struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(libraryResponse.Body.Bytes(), &library); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	created := request(t, handler, http.MethodPost, "/api/v1/projects", map[string]any{"libraryId": library.ID, "name": "Local project", "rootPath": root, "excludePatterns": []string{"tmp/"}, "execution": map[string]any{"executor": "codex", "codexModel": "safe-model"}}, "desktop-test")
	if created.Code != http.StatusCreated {
		t.Fatalf("project: %d %s", created.Code, created.Body.String())
	}
	var project struct {
		ID        string         `json:"id"`
		RootPath  string         `json:"rootPath"`
		Execution map[string]any `json:"execution"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	if project.ID == "" || project.RootPath != root {
		t.Fatalf("project response: %+v", project)
	}
	if _, exists := project.Execution["codexArguments"]; exists {
		t.Fatal("arbitrary codex arguments leaked into execution contract")
	}
	if forbidden := request(t, handler, http.MethodGet, "/api/v1/projects", nil, "bad-token"); forbidden.Code != http.StatusUnauthorized {
		t.Fatalf("desktop auth: %d", forbidden.Code)
	}
	accepted := request(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID+"/scan", nil, "desktop-test")
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("scan: %d %s", accepted.Code, accepted.Body.String())
	}
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(accepted.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		items := request(t, handler, http.MethodGet, "/api/v1/projects/"+project.ID+"/tasks", nil, "desktop-test")
		if items.Code != http.StatusOK {
			t.Fatalf("tasks: %d %s", items.Code, items.Body.String())
		}
		if bytes.Contains(items.Body.Bytes(), []byte(`"status":"completed"`)) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	snapshots := request(t, handler, http.MethodGet, "/api/v1/projects/"+project.ID+"/snapshots", nil, "desktop-test")
	if snapshots.Code != http.StatusOK || !bytes.Contains(snapshots.Body.Bytes(), []byte(`"contentHash"`)) {
		t.Fatalf("snapshots: %d %s", snapshots.Code, snapshots.Body.String())
	}
}

func TestArchitectureScanHonorsLifecycleCancellation(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	library, err := server.Store.CreateLibrary(ctx, "Architecture cancellation", "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := server.Store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Cancelled project", RootPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	task, err := server.Store.CreateArchitectureTask(ctx, project.ID, "scan")
	if err != nil {
		t.Fatal(err)
	}
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()
	server.runArchitectureScan(cancelledCtx, task.ID, project)
	item, err := server.Store.GetArchitectureTask(ctx, task.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "queued" || item.SnapshotID != "" {
		t.Fatalf("cancelled lifecycle started or committed scan: %+v", item)
	}
	snapshots, err := server.Store.ListArchitectureSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("cancelled lifecycle committed snapshots: %+v", snapshots)
	}
	if _, err := server.Store.CancelArchitectureTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}

	task, err = server.Store.CreateArchitectureTask(ctx, project.ID, "scan")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Store.CancelArchitectureTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	server.runArchitectureScan(ctx, task.ID, project)
	item, err = server.Store.GetArchitectureTask(ctx, task.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "cancelled" || item.SnapshotID != "" {
		t.Fatalf("cancelled task started or committed scan: %+v", item)
	}
}

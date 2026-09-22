package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/architecture"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func waitArchitectureTask(t *testing.T, server *Server, projectID, taskID string, want ...string) model.ArchitectureTask {
	t.Helper()
	// A real Archify render validates and delivers three typed diagrams and may
	// also run the optional visual check. Keep this bounded, but do not make a
	// healthy local renderer race an arbitrary eight-second test budget.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		item, err := server.Store.GetArchitectureTask(context.Background(), taskID, false)
		if err == nil {
			for _, status := range want { if item.Status == status { return item } }
		}
		time.Sleep(25 * time.Millisecond)
	}
	item, _ := server.Store.GetArchitectureTask(context.Background(), taskID, false)
	t.Fatalf("task %s did not reach %v: %+v", projectID, want, item)
	return item
}

func TestArchitectureAnalysisContextCancellationFinalizesTaskWithoutPublishing(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	library, err := server.Store.CreateLibrary(ctx, "Analysis cancellation", "")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project, err := server.Store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Cancelled analysis", RootPath: root, Execution: model.ProjectExecutionConfig{Executor: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := server.Store.CreateArchitectureTask(ctx, project.ID, "scan")
	if err != nil {
		t.Fatal(err)
	}
	go server.runArchitectureScan(ctx, scan.ID, project)
	waitArchitectureTask(t, server, project.ID, scan.ID, "completed")

	started := make(chan struct{})
	server.ArchitectureChain = architecture.AnalysisChain{Codex: architecture.FunctionAnalyzer{
		ExecutorName: "codex",
		Check:        func(context.Context, model.Project) bool { return true },
		Run: func(runContext context.Context, _ model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error) {
			close(started)
			<-runContext.Done()
			return model.ArchitectureAnalysis{}, runContext.Err()
		},
	}}
	analysisTask, err := server.Store.CreateArchitectureTask(ctx, project.ID, "analysis")
	if err != nil {
		t.Fatal(err)
	}
	analysisContext, cancel := context.WithCancel(ctx)
	go server.runArchitectureAnalysis(analysisContext, analysisTask.ID, project)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("analysis executor did not start")
	}
	cancel()
	finished := waitArchitectureTask(t, server, project.ID, analysisTask.ID, "cancelled", "failed")
	if finished.Status != "cancelled" && finished.Status != "failed" {
		t.Fatalf("cancelled analysis remained non-terminal: %+v", finished)
	}
	stored, err := server.Store.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CurrentAnalysisID != "" || stored.AnalysisStatus == "current" {
		t.Fatalf("cancelled analysis published current state: %+v", stored)
	}
}

func TestArchitectureGenerationHTTPFullChainAndExports(t *testing.T) {
	server, handler := testServer(t)
	ctx := context.Background()
	library, err := server.Store.CreateLibrary(ctx, "Generation", "")
	if err != nil { t.Fatal(err) }
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o640); err != nil { t.Fatal(err) }
	project, err := server.Store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Generated", RootPath: root, Execution: model.ProjectExecutionConfig{Executor: "codex"}})
	if err != nil { t.Fatal(err) }
	server.ArchitectureChain = architecture.AnalysisChain{Codex: architecture.FunctionAnalyzer{ExecutorName: "codex", Check: func(context.Context, model.Project) bool { return true }, Run: func(_ context.Context, req model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error) {
		file := req.Snapshot.Files[0]
		return model.ArchitectureAnalysis{SchemaVersion: "kah.architecture.analysis.v1", Evidence: []model.AnalysisEvidence{{ID: "e-main", Path: file.Path, LineStart: file.LineStart, LineEnd: file.LineEnd, ContentHash: file.ContentHash, Kind: "source"}}, Facts: []model.AnalysisFact{{ID: "f-main", Subject: "project", Predicate: "contains", Object: file.Path, Evidence: []string{"e-main"}}}, Coverage: req.Snapshot.Coverage}, nil
	}}}
	scan, err := server.Store.CreateArchitectureTask(ctx, project.ID, "scan")
	if err != nil { t.Fatal(err) }
	go server.runArchitectureScan(ctx, scan.ID, project)
	waitArchitectureTask(t, server, project.ID, scan.ID, "completed")
	accepted := request(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID+"/generate", nil, "desktop-test")
	if accepted.Code != http.StatusAccepted { t.Fatalf("generate: %d %s", accepted.Code, accepted.Body.String()) }
	var queued model.ArchitectureTask
	if err := json.Unmarshal(accepted.Body.Bytes(), &queued); err != nil { t.Fatal(err) }
	waitArchitectureTask(t, server, project.ID, queued.ID, "completed")
	current, err := server.Store.GetProject(ctx, project.ID)
	if err != nil || current.AnalysisStatus != "current" || current.CurrentAnalysisID == "" { t.Fatalf("current analysis: %+v %v", current, err) }
	artifacts, err := server.Store.ListArchitectureArtifacts(ctx, project.ID, current.CurrentAnalysisID)
	if err != nil || len(artifacts) != 5 { t.Fatalf("artifacts: %+v %v", artifacts, err) }
	png := request(t, handler, http.MethodGet, "/api/v1/projects/"+project.ID+"/snapshots/"+current.CurrentAnalysisID+"/artifacts/png", nil, "desktop-test")
	if png.Code != http.StatusOK || !bytes.HasPrefix(png.Body.Bytes(), []byte{0x89, 'P', 'N', 'G'}) { t.Fatalf("png export: %d %x", png.Code, png.Body.Bytes()[:minInt(8, png.Body.Len())]) }
	diff := request(t, handler, http.MethodGet, "/api/v1/projects/"+project.ID+"/versions/"+current.CurrentAnalysisID+"/diff", nil, "desktop-test")
	if diff.Code != http.StatusOK { t.Fatalf("diff: %d %s", diff.Code, diff.Body.String()) }
}

func TestArchitectureGenerationFailurePreservesPreviousCurrent(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	library, err := server.Store.CreateLibrary(ctx, "Failure", "")
	if err != nil { t.Fatal(err) }
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o640)
	project, err := server.Store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Failure", RootPath: root, Execution: model.ProjectExecutionConfig{Executor: "codex"}})
	if err != nil { t.Fatal(err) }
	server.ArchitectureChain = architecture.AnalysisChain{Codex: architecture.FunctionAnalyzer{ExecutorName: "codex", Check: func(context.Context, model.Project) bool { return true }, Run: func(context.Context, model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error) { return model.ArchitectureAnalysis{}, errors.New("provider fixture failed") }}}
	scan, _ := server.Store.CreateArchitectureTask(ctx, project.ID, "scan"); go server.runArchitectureScan(ctx, scan.ID, project); waitArchitectureTask(t, server, project.ID, scan.ID, "completed")
	generate, _ := server.Store.CreateArchitectureTask(ctx, project.ID, "analysis"); go server.runArchitectureAnalysis(ctx, generate.ID, project); waitArchitectureTask(t, server, project.ID, generate.ID, "failed")
	stored, _ := server.Store.GetProject(ctx, project.ID)
	if stored.AnalysisStatus != "stale" || stored.CurrentAnalysisID != "" || stored.AnalysisError == "" { t.Fatalf("failure state: %+v", stored) }
}

func TestArchitectureAgentHTTPLeaseAuthScopeAndHeartbeats(t *testing.T) {
	server, handler := testServer(t)
	ctx := context.Background()
	library, err := server.Store.CreateLibrary(ctx, "Agent HTTP", "")
	if err != nil { t.Fatal(err) }
	project, err := server.Store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Scoped", RootPath: t.TempDir()})
	if err != nil { t.Fatal(err) }
	registered := request(t, handler, http.MethodPost, "/api/v1/architecture/agents", map[string]any{"name": "HTTP worker", "capabilities": []string{"architecture.analysis"}, "projectIds": []string{project.ID}}, "desktop-test")
	if registered.Code != http.StatusCreated { t.Fatalf("register: %d %s", registered.Code, registered.Body.String()) }
	var credential model.ArchitectureAgentCredential
	if err := json.Unmarshal(registered.Body.Bytes(), &credential); err != nil || credential.Secret == "" { t.Fatalf("credential: %v %s", err, registered.Body.String()) }
	restricted := request(t, handler, http.MethodPost, "/api/v1/architecture/agents", map[string]any{"name": "Scoped non-analysis worker", "capabilities": []string{"document.read"}, "projectIds": []string{project.ID}}, "desktop-test")
	if restricted.Code != http.StatusCreated { t.Fatalf("restricted registration: %d %s", restricted.Code, restricted.Body.String()) }
	var restrictedCredential model.ArchitectureAgentCredential
	if err := json.Unmarshal(restricted.Body.Bytes(), &restrictedCredential); err != nil || restrictedCredential.Secret == "" { t.Fatalf("restricted credential: %v %s", err, restricted.Body.String()) }
	task, err := server.Store.CreateArchitectureTask(ctx, project.ID, "analysis")
	if err != nil { t.Fatal(err) }
	if noClaim := request(t, handler, http.MethodPost, "/api/v1/architecture/agent/tasks/claim", nil, restrictedCredential.Secret); noClaim.Code != http.StatusNoContent { t.Fatalf("non-analysis capability claimed task: %d %s", noClaim.Code, noClaim.Body.String()) }
	if forbidden := request(t, handler, http.MethodPost, "/api/v1/architecture/agent/tasks/claim", nil, "desktop-test"); forbidden.Code != http.StatusUnauthorized { t.Fatalf("desktop token reached agent route: %d %s", forbidden.Code, forbidden.Body.String()) }
	claimedResponse := request(t, handler, http.MethodPost, "/api/v1/architecture/agent/tasks/claim", nil, credential.Secret)
	if claimedResponse.Code != http.StatusOK { t.Fatalf("claim: %d %s", claimedResponse.Code, claimedResponse.Body.String()) }
	var lease model.ArchitectureAgentLease
	if err := json.Unmarshal(claimedResponse.Body.Bytes(), &lease); err != nil || lease.Task.ID != task.ID || lease.LeaseToken == "" || lease.Task.Generation == 0 { t.Fatalf("lease: %v %+v", err, lease) }
	stale := request(t, handler, http.MethodPost, "/api/v1/architecture/agent/tasks/heartbeat", map[string]any{"taskId": task.ID, "leaseToken": "stale"}, credential.Secret)
	if stale.Code != http.StatusConflict { t.Fatalf("stale heartbeat: %d %s", stale.Code, stale.Body.String()) }
	heartbeat := request(t, handler, http.MethodPost, "/api/v1/architecture/agent/tasks/heartbeat", map[string]any{"taskId": task.ID, "leaseToken": lease.LeaseToken}, credential.Secret)
	if heartbeat.Code != http.StatusNoContent { t.Fatalf("heartbeat: %d %s", heartbeat.Code, heartbeat.Body.String()) }
	failure := request(t, handler, http.MethodPost, "/api/v1/architecture/agent/tasks/failure", map[string]any{"taskId": task.ID, "leaseToken": lease.LeaseToken, "message": "fixture failure"}, credential.Secret)
	if failure.Code != http.StatusOK { t.Fatalf("failure report: %d %s", failure.Code, failure.Body.String()) }
}

func TestMCPArchitectureExecutorClaimsHeartbeatsAndReadsSubmittedResult(t *testing.T) {
	server, _ := testServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	library, err := server.Store.CreateLibrary(ctx, "MCP executor", "")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project, err := server.Store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "MCP project", RootPath: root, AllowRemoteCode: true, Execution: model.ProjectExecutionConfig{Executor: "mcp"}})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := server.Store.RegisterArchitectureAgentCredential(ctx, model.ArchitectureAgentRegistration{Name: "MCP worker", Capabilities: []string{"architecture.analysis"}, ProjectIDs: []string{project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := server.Store.AuthenticateArchitectureAgent(ctx, credential.Secret)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := server.Store.CreateArchitectureTask(ctx, project.ID, "scan")
	if err != nil {
		t.Fatal(err)
	}
	go server.runArchitectureScan(ctx, scan.ID, project)
	waitArchitectureTask(t, server, project.ID, scan.ID, "completed")
	snapshot, err := server.Store.GetLatestArchitectureSnapshot(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := model.ArchitectureAnalysisRequest{Project: project, Snapshot: snapshot, Prompt: "fixture"}
	workerDone := make(chan error, 1)
	go func() {
		for {
			lease, claimErr := server.Store.ClaimNextArchitectureTask(ctx, agent, time.Minute)
			if claimErr == nil {
				input := lease.Input
				if input == nil || input.Snapshot.ID != snapshot.ID {
					workerDone <- errors.New("MCP lease did not carry the analysis input")
					return
				}
				file := input.Snapshot.Files[0]
				analysis := model.ArchitectureAnalysis{SchemaVersion: "kah.architecture.analysis.v1", ProjectID: project.ID, SnapshotID: snapshot.ID, Evidence: []model.AnalysisEvidence{{ID: "mcp-evidence", Path: file.Path, LineStart: file.LineStart, LineEnd: file.LineEnd, ContentHash: file.ContentHash, Kind: "source"}}, Facts: []model.AnalysisFact{{ID: "mcp-fact", Subject: "project", Predicate: "contains", Object: file.Path, Evidence: []string{"mcp-evidence"}}}, Coverage: input.Snapshot.Coverage}
				_, submitErr := server.Store.SubmitArchitectureAgentResult(ctx, lease.ID, agent.ID, lease.LeaseToken, analysis, nil, lease.Generation)
				workerDone <- submitErr
				return
			}
			if !errors.Is(claimErr, sql.ErrNoRows) {
				workerDone <- claimErr
				return
			}
			select {
			case <-ctx.Done():
				workerDone <- ctx.Err()
				return
			case <-time.After(25 * time.Millisecond):
			}
		}
	}()
	result, err := server.runMCPArchitectureAgent(ctx, request)
	if err != nil || result.SnapshotID != snapshot.ID {
		t.Fatalf("MCP executor result=%+v err=%v", result, err)
	}
	if workerErr := <-workerDone; workerErr != nil {
		t.Fatalf("MCP worker: %v", workerErr)
	}
	stored, err := server.Store.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CurrentAnalysisID != "" {
		t.Fatalf("MCP handoff published current before API artifact validation: %+v", stored)
	}
	if task, taskErr := server.Store.ListArchitectureTasks(ctx, project.ID); taskErr != nil || len(task) < 2 {
		t.Fatalf("MCP task history: %v %v", task, taskErr)
	}
}

func TestArchitectureSchedulerRunsScanThenConfiguredGenerationAndMergesMissedRun(t *testing.T) {
	server, _ := testServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	library, err := server.Store.CreateLibrary(ctx, "Scheduler", "")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project, err := server.Store.CreateProject(ctx, model.Project{
		LibraryID: library.ID, Name: "Scheduled generation", RootPath: root,
		ScheduleKind: "interval", ScanPeriodSeconds: 3600,
		ScheduleAnchor: time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
		Execution: model.ProjectExecutionConfig{Executor: "codex"},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.ArchitectureChain = architecture.AnalysisChain{
		Codex: architecture.FunctionAnalyzer{
			ExecutorName: "codex",
			Check:        func(context.Context, model.Project) bool { return true },
			Run: func(_ context.Context, request model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error) {
				file := request.Snapshot.Files[0]
				return model.ArchitectureAnalysis{
					SchemaVersion: "kah.architecture.analysis.v1",
					Evidence: []model.AnalysisEvidence{{ID: "scheduled-evidence", Path: file.Path, LineStart: file.LineStart, LineEnd: file.LineEnd, ContentHash: file.ContentHash, Kind: "source"}},
					Facts:    []model.AnalysisFact{{ID: "scheduled-fact", Subject: "project", Predicate: "contains", Object: file.Path, Evidence: []string{"scheduled-evidence"}}},
					Coverage: request.Snapshot.Coverage,
				}, nil
			},
		},
	}
	server.SetLifecycleContext(ctx)
	if err := server.architectureScheduleTick(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// The durable schedule advances before the goroutine starts. A second tick
	// during the same period must therefore not enqueue a duplicate catch-up.
	if err := server.architectureScheduleTick(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	items, err := server.Store.ListArchitectureTasks(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != "scan" {
		t.Fatalf("scheduler enqueued duplicate or wrong task: %+v", items)
	}
	waitArchitectureTask(t, server, project.ID, items[0].ID, "completed")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		items, err = server.Store.ListArchitectureTasks(ctx, project.ID)
		if err == nil {
			for _, item := range items {
				if item.Kind == "analysis" && item.Status == "completed" {
					projectState, stateErr := server.Store.GetProject(ctx, project.ID)
					if stateErr != nil {
						t.Fatal(stateErr)
					}
					if projectState.AnalysisStatus != "current" || projectState.CurrentAnalysisID == "" {
						t.Fatalf("scheduled generation did not publish current: %+v", projectState)
					}
					return
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("scheduled scan never created a completed analysis task: %+v", items)
}

func minInt(a, b int) int { if a < b { return a }; return b }

package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func architectureSnapshot(projectID, hash, path string) model.ArchitectureSnapshot {
	return model.ArchitectureSnapshot{ProjectID: projectID, ContentHash: hash, GitChangedPaths: []string{}, Coverage: model.ArchitectureCoverage{ByLanguage: map[string]int{}, IgnoredReasons: map[string]int{}}, Files: []model.ArchitectureFile{{Path: path, Language: "go", LineStart: 1, LineEnd: 2, LineCount: 2, ContentHash: hash}}, Modules: []model.ArchitectureModule{{Name: "app", Language: "go", RootPath: ".", Dependencies: []string{}, DependencyVersions: map[string]string{}, DependencyFiles: []string{path}, EntryPoints: []string{}, ContainerFiles: []string{}, EvidencePaths: []string{path}}}}
}

func architectureSnapshotWithFiles(projectID, hash string, fileHashes map[string]string) model.ArchitectureSnapshot {
	snapshot := architectureSnapshot(projectID, hash, "placeholder.go")
	snapshot.Files = nil
	for path, fileHash := range fileHashes {
		snapshot.Files = append(snapshot.Files, model.ArchitectureFile{Path: path, Language: "go", LineStart: 1, LineEnd: 1, LineCount: 1, ContentHash: fileHash})
	}
	snapshot.Modules = []model.ArchitectureModule{}
	return snapshot
}

func TestArchitectureSnapshotPersistenceAndCurrentChange(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dataRoot := t.TempDir()
	store, err := Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Architecture", "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Project", RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	a := architectureSnapshot(project.ID, "hash-a", "main.go")
	savedA, changed, err := store.SaveArchitectureSnapshot(ctx, a)
	if err != nil || !changed {
		t.Fatalf("save A: %+v changed=%v err=%v", savedA, changed, err)
	}
	if _, changed, err := store.SaveArchitectureSnapshot(ctx, a); err != nil || changed {
		t.Fatalf("repeat A changed=%v err=%v", changed, err)
	}
	b := architectureSnapshot(project.ID, "hash-b", "main.go")
	if _, changed, err := store.SaveArchitectureSnapshot(ctx, b); err != nil || !changed {
		t.Fatalf("save B changed=%v err=%v", changed, err)
	}
	aAgain, changed, err := store.SaveArchitectureSnapshot(ctx, a)
	if err != nil || !changed || aAgain.ID != savedA.ID || aAgain.Status == "unchanged" {
		t.Fatalf("A after B should be current changed: %+v changed=%v err=%v", aAgain, changed, err)
	}
	project, err = store.GetProject(ctx, project.ID)
	if err != nil || project.LastSnapshotID != savedA.ID || project.LastScanChanged == nil || !*project.LastScanChanged {
		t.Fatalf("project current snapshot state: %+v err=%v", project, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.GetArchitectureSnapshot(ctx, project.ID, savedA.ID)
	if err != nil || len(loaded.Files) != 1 || loaded.Files[0].ContentHash != "hash-a" {
		t.Fatalf("snapshot did not survive reopen: %+v err=%v", loaded, err)
	}
}

func TestArchitectureTaskLeaseRaceExpiryCancellationAndAtomicCommit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Tasks", "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Project", RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateArchitectureTask(ctx, project.ID, "scan")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateArchitectureTask(ctx, project.ID, "scan"); !errors.Is(err, ErrProjectTaskActive) {
		t.Fatalf("duplicate active task err=%v", err)
	}
	var claimed []model.ArchitectureTask
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, owner := range []string{"one", "two"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			item, claimErr := store.ClaimArchitectureTask(ctx, task.ID, owner, time.Minute)
			if claimErr == nil {
				mu.Lock()
				claimed = append(claimed, item)
				mu.Unlock()
			}
		}(owner)
	}
	wg.Wait()
	if len(claimed) != 1 {
		t.Fatalf("expected one atomic claimant, got %d", len(claimed))
	}
	if _, err := store.SubmitArchitectureTask(ctx, task.ID, claimed[0].LeaseOwner, claimed[0].LeaseToken, "completed", "missing", "late"); err != nil {
		t.Fatal(err)
	}
	// A completed task no longer blocks a new run.
	task, err = store.CreateArchitectureTask(ctx, project.ID, "scan")
	if err != nil {
		t.Fatal(err)
	}
	claimedTask, err := store.ClaimArchitectureTask(ctx, task.ID, "expiry", 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := store.SubmitArchitectureTask(ctx, task.ID, "expiry", claimedTask.LeaseToken, "completed", "bad", "expired"); !errors.Is(err, ErrTaskLeaseExpired) {
		t.Fatalf("expired submit err=%v", err)
	}
	reclaimed, err := store.ClaimArchitectureTask(ctx, task.ID, "recovered", time.Minute)
	if err != nil || reclaimed.LeaseOwner != "recovered" {
		t.Fatalf("expired claim recovery: %+v err=%v", reclaimed, err)
	}
	if _, err := store.CancelArchitectureTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateArchitectureTask(ctx, project.ID, "scan"); err != nil {
		t.Fatalf("cancelled task did not release active slot: %v", err)
	}
	// A cancellation cannot commit an immutable snapshot, even with the old token.
	if _, _, err := store.SaveArchitectureSnapshotForTask(ctx, task.ID, "recovered", reclaimed.LeaseToken, architectureSnapshot(project.ID, "cancelled", "main.go")); !errors.Is(err, ErrTaskCancelled) && !errors.Is(err, ErrTaskLeaseExpired) {
		t.Fatalf("cancelled snapshot commit err=%v", err)
	}
}

func TestArchitectureAnalysisEvidenceValidation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Analysis", "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Project", RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := architectureSnapshot(project.ID, "hash", "main.go")
	saved, _, err := store.SaveArchitectureSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	valid := model.ArchitectureAnalysis{SchemaVersion: "kah.architecture.analysis.v1", ProjectID: project.ID, SnapshotID: saved.ID, Evidence: []model.AnalysisEvidence{{ID: "e1", Path: "main.go", LineStart: 1, LineEnd: 2, ContentHash: "hash", Kind: "source"}}, Facts: []model.AnalysisFact{{ID: "f1", Subject: "app", Predicate: "language", Object: "go", Evidence: []string{"e1"}}}}
	if err := store.SaveArchitectureAnalysis(ctx, valid); err != nil {
		t.Fatalf("valid analysis: %v", err)
	}
	valid.Evidence[0].ContentHash = "wrong"
	if err := store.SaveArchitectureAnalysis(ctx, valid); err == nil {
		t.Fatal("invalid evidence hash was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestArchitectureAnalysisValidatesIncrementalHashesAndEntityEvidence(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Incremental", "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Project", RootPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	base, _, err := store.SaveArchitectureSnapshot(ctx, architectureSnapshotWithFiles(project.ID, "base-snapshot", map[string]string{
		"changed.go": "old", "unchanged.go": "same", "deleted.go": "gone",
	}))
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := store.SaveArchitectureSnapshot(ctx, architectureSnapshotWithFiles(project.ID, "current-snapshot", map[string]string{
		"added.go": "added", "changed.go": "new", "unchanged.go": "same",
	}))
	if err != nil {
		t.Fatal(err)
	}
	valid := model.ArchitectureAnalysis{
		SchemaVersion: "kah.architecture.analysis.v1", ProjectID: project.ID, SnapshotID: current.ID,
		Evidence:    []model.AnalysisEvidence{{ID: "e-added", Path: "added.go", LineStart: 1, LineEnd: 1, ContentHash: "added", Kind: "source"}},
		Facts:       []model.AnalysisFact{{ID: "fact-1", Subject: "app", Predicate: "contains", Object: "added.go", Evidence: []string{"e-added"}}},
		Inferences:  []model.AnalysisInference{{ID: "inference-1", Statement: "the app has an added Go file", Evidence: []string{"e-added"}, Confidence: 0.8}},
		Incremental: model.AnalysisIncremental{BaseSnapshotID: base.ID, Added: []string{"added.go"}, Changed: []string{"changed.go"}, Deleted: []string{"deleted.go"}, Unchanged: []string{"unchanged.go"}},
	}
	if err := store.SaveArchitectureAnalysis(ctx, valid); err != nil {
		t.Fatalf("valid incremental analysis: %v", err)
	}
	invalid := valid
	invalid.Facts = []model.AnalysisFact{{ID: "fact-1", Subject: "app", Predicate: "contains", Object: "added.go"}}
	if err := store.SaveArchitectureAnalysis(ctx, invalid); err == nil {
		t.Fatal("fact without evidence was accepted")
	}
	invalid = valid
	invalid.Inferences[0].ID = "fact-1"
	if err := store.SaveArchitectureAnalysis(ctx, invalid); err == nil {
		t.Fatal("duplicate fact/inference ID was accepted")
	}
	invalid = valid
	invalid.Incremental.Changed = []string{"unchanged.go"}
	if err := store.SaveArchitectureAnalysis(ctx, invalid); err == nil {
		t.Fatal("unchanged file was accepted as changed")
	}
	invalid = valid
	invalid.Incremental.Added = []string{"added.go", "changed.go"}
	if err := store.SaveArchitectureAnalysis(ctx, invalid); err == nil {
		t.Fatal("base-present file was accepted as added")
	}
}

func TestArchitectureAgentCredentialScopeLeaseAndStaleGeneration(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil { t.Fatal(err) }
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Agents", "")
	if err != nil { t.Fatal(err) }
	project, err := store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Agent project", RootPath: t.TempDir()})
	if err != nil { t.Fatal(err) }
	credential, err := store.RegisterArchitectureAgentCredential(ctx, model.ArchitectureAgentRegistration{Name: "worker", Capabilities: []string{"architecture.analysis"}, ProjectIDs: []string{project.ID}})
	if err != nil { t.Fatal(err) }
	authenticated, err := store.AuthenticateArchitectureAgent(ctx, credential.Secret)
	if err != nil || authenticated.ID != credential.Agent.ID { t.Fatalf("agent authentication: %+v %v", authenticated, err) }
	if _, err := store.RegisterArchitectureAgentCredential(ctx, model.ArchitectureAgentRegistration{Name: "unscoped"}); err == nil { t.Fatal("unscoped agent was accepted") }
	task, err := store.CreateArchitectureTask(ctx, project.ID, "analysis")
	if err != nil { t.Fatal(err) }
	claimed, err := store.ClaimNextArchitectureTask(ctx, authenticated, time.Minute)
	if err != nil || claimed.ID != task.ID || claimed.LeaseToken == "" { t.Fatalf("agent claim: %+v %v", claimed, err) }
	if err := store.RenewArchitectureTask(ctx, task.ID, "agent:"+authenticated.ID, claimed.LeaseToken, time.Minute); err != nil { t.Fatal(err) }
	if _, err := store.SubmitArchitectureAgentFailure(ctx, task.ID, authenticated.ID, "old-token", "late"); !errors.Is(err, ErrTaskLeaseExpired) { t.Fatalf("stale token accepted: %v", err) }
	if _, err := store.SubmitArchitectureAgentFailure(ctx, task.ID, authenticated.ID, claimed.LeaseToken, "failed"); err != nil { t.Fatal(err) }
}

func TestArchitectureScheduleDueAndCatchUpAdvancesOnce(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil { t.Fatal(err) }
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Schedules", "")
	if err != nil { t.Fatal(err) }
	anchor := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	project, err := store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Scheduled", RootPath: t.TempDir(), ScheduleKind: "interval", ScanPeriodSeconds: 3600, ScheduleAnchor: anchor})
	if err != nil { t.Fatal(err) }
	due, err := store.ListDueArchitectureProjects(ctx, time.Now().UTC())
	if err != nil || len(due) != 1 || due[0].ProjectID != project.ID { t.Fatalf("due schedule: %+v %v", due, err) }
	if err := store.MarkArchitectureSchedule(ctx, project.ID, due[0].NextAt, time.Now().UTC()); err != nil { t.Fatal(err) }
	if due, err = store.ListDueArchitectureProjects(ctx, time.Now().UTC()); err != nil || len(due) != 0 { t.Fatalf("schedule did not advance: %+v %v", due, err) }
}

func TestArchitectureAgentResultAtomicLeaseRejectLeavesNoAnalysisArtifactOrCurrent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil { t.Fatal(err) }
	defer store.Close()
	library, err := store.CreateLibrary(ctx, "Atomic agent result", "")
	if err != nil { t.Fatal(err) }
	project, err := store.CreateProject(ctx, model.Project{LibraryID: library.ID, Name: "Atomic", RootPath: t.TempDir()})
	if err != nil { t.Fatal(err) }
	snapshot, _, err := store.SaveArchitectureSnapshot(ctx, architectureSnapshot(project.ID, "atomic-hash", "main.go"))
	if err != nil { t.Fatal(err) }
	agentCredential, err := store.RegisterArchitectureAgentCredential(ctx, model.ArchitectureAgentRegistration{Name: "atomic-agent", ProjectIDs: []string{project.ID}})
	if err != nil { t.Fatal(err) }
	agent, err := store.AuthenticateArchitectureAgent(ctx, agentCredential.Secret)
	if err != nil { t.Fatal(err) }
	valid := model.ArchitectureAnalysis{SchemaVersion: "kah.architecture.analysis.v1", ProjectID: project.ID, SnapshotID: snapshot.ID, Evidence: []model.AnalysisEvidence{{ID: "e", Path: "main.go", LineStart: 1, LineEnd: 2, ContentHash: "atomic-hash", Kind: "source"}}, Facts: []model.AnalysisFact{{ID: "f", Subject: "app", Predicate: "contains", Object: "main.go", Evidence: []string{"e"}}}}
	inputs := []ArchitectureArtifactInput{{Kind: "document", FileName: "architecture.md", ContentType: "text/markdown", Data: []byte("atomic")}}
	assertUnchanged := func(label string) {
		t.Helper()
		if _, err := store.GetArchitectureAnalysis(ctx, project.ID, snapshot.ID); !errors.Is(err, sql.ErrNoRows) { t.Fatalf("%s left analysis: %v", label, err) }
		if items, err := store.ListArchitectureArtifacts(ctx, project.ID, snapshot.ID); err != nil || len(items) != 0 { t.Fatalf("%s left artifacts: %+v %v", label, items, err) }
		current, err := store.GetProject(ctx, project.ID); if err != nil || current.CurrentAnalysisID != "" || current.AnalysisStatus != "" { t.Fatalf("%s changed current state: %+v %v", label, current, err) }
	}
	lateTask, err := store.CreateArchitectureTask(ctx, project.ID, "analysis")
	if err != nil { t.Fatal(err) }
	lateClaim, err := store.ClaimArchitectureTask(ctx, lateTask.ID, "agent:"+agent.ID, 10*time.Millisecond)
	if err != nil { t.Fatal(err) }
	time.Sleep(30 * time.Millisecond)
	if _, err := store.SubmitArchitectureAgentResult(ctx, lateTask.ID, agent.ID, lateClaim.LeaseToken, valid, inputs, lateClaim.Generation); !errors.Is(err, ErrTaskLeaseExpired) { t.Fatalf("expired result: %v", err) }
	assertUnchanged("expired")
	if _, err := store.CancelArchitectureTask(ctx, lateTask.ID); err != nil { t.Fatal(err) }
	cancelTask, err := store.CreateArchitectureTask(ctx, project.ID, "analysis")
	if err != nil { t.Fatal(err) }
	cancelClaim, err := store.ClaimArchitectureTask(ctx, cancelTask.ID, "agent:"+agent.ID, time.Minute)
	if err != nil { t.Fatal(err) }
	if _, err := store.CancelArchitectureTask(ctx, cancelTask.ID); err != nil { t.Fatal(err) }
	if _, err := store.SubmitArchitectureAgentResult(ctx, cancelTask.ID, agent.ID, cancelClaim.LeaseToken, valid, inputs, cancelClaim.Generation); !errors.Is(err, ErrTaskCancelled) { t.Fatalf("cancelled result: %v", err) }
	assertUnchanged("cancelled")
	generationTask, err := store.CreateArchitectureTask(ctx, project.ID, "analysis")
	if err != nil { t.Fatal(err) }
	generationClaim, err := store.ClaimArchitectureTask(ctx, generationTask.ID, "agent:"+agent.ID, time.Minute)
	if err != nil { t.Fatal(err) }
	if _, err := store.SubmitArchitectureAgentResult(ctx, generationTask.ID, agent.ID, generationClaim.LeaseToken, valid, inputs, generationClaim.Generation+1); !errors.Is(err, ErrTaskLeaseExpired) { t.Fatalf("stale generation result: %v", err) }
	assertUnchanged("generation")
	secondSnapshot, _, err := store.SaveArchitectureSnapshot(ctx, architectureSnapshot(project.ID, "atomic-hash-two", "main.go"))
	if err != nil { t.Fatal(err) }
	wrongSnapshot := valid
	wrongSnapshot.SnapshotID = secondSnapshot.ID
	wrongSnapshot.Evidence = []model.AnalysisEvidence{{ID: "e", Path: "main.go", LineStart: 1, LineEnd: 2, ContentHash: "atomic-hash-two", Kind: "source"}}
	wrongTask, err := store.CreateArchitectureTask(ctx, project.ID, "mcp", model.ArchitectureAnalysisRequest{Project: project, Snapshot: snapshot})
	if err != nil { t.Fatal(err) }
	wrongClaim, err := store.ClaimArchitectureTask(ctx, wrongTask.ID, "agent:"+agent.ID, time.Minute)
	if err != nil { t.Fatal(err) }
	if _, err := store.SubmitArchitectureAgentResult(ctx, wrongTask.ID, agent.ID, wrongClaim.LeaseToken, wrongSnapshot, nil, wrongClaim.Generation); err == nil || !strings.Contains(err.Error(), "does not match task input") {
		t.Fatalf("stale snapshot result accepted: %v", err)
	}
	if _, err := store.GetArchitectureAnalysis(ctx, project.ID, secondSnapshot.ID); !errors.Is(err, sql.ErrNoRows) { t.Fatalf("stale snapshot result left analysis: %v", err) }
}

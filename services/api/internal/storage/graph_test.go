package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestDeterministicGraphPositionMatchesDesktop(t *testing.T) {
	position := deterministicGraphPosition("kah://knowledge/abc?revision=2")
	if position.XPct != 73.236 || position.YPct != 67.57 {
		t.Fatalf("cross-runtime deterministic position changed: %#v", position)
	}
}

func TestGraphCollisionResolutionSeparatesNewNodes(t *testing.T) {
	base := model.GraphPosition{XPct: 50, YPct: 50}
	resolved := resolveGraphPositionCollision(base, []model.GraphPosition{{XPct: 50.5, YPct: 50.5}}, 12)
	if graphPositionsOverlap(resolved, model.GraphPosition{XPct: 50.5, YPct: 50.5}, 12) {
		t.Fatalf("resolved graph position still overlaps: %+v", resolved)
	}
}

func publishGraphKnowledge(t *testing.T, store *Store, libraryID, key string, payload model.KnowledgePayload) model.KnowledgeRevision {
	t.Helper()
	ctx := context.Background()
	submission, _, err := store.CreateKnowledgeDraft(ctx, KnowledgeDraftInput{LibraryID: libraryID, ClientSubmissionID: key, Mode: "create", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReviewKAHSubmission(ctx, submission.ID, "graph-test", "approve", ""); err != nil {
		t.Fatal(err)
	}
	revision, err := store.PublishKAHSubmission(ctx, submission.ID)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestGraphMigrationAndFractionalPositionsAreAtomic(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, table := range []string{"graph_workspaces", "graph_node_positions", "graph_workspace_preferences"} {
		var name string
		if err := store.DB.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil || name != table {
			t.Fatalf("graph migration missing %s: name=%q err=%v", table, name, err)
		}
	}
	library, err := store.CreateLibrary(ctx, "Graph positions", "")
	if err != nil {
		t.Fatal(err)
	}
	revision := publishGraphKnowledge(t, store, library.ID, "graph-position", model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "Fractional graph position", Description: "Position precision", Language: "en",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "Claim", Content: "Graph coordinates retain decimal precision."}},
	})
	workspace, err := store.GetOrCreateDefaultGraphWorkspace(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := graphKnowledgeNodeID(revision.URI, revision.Revision)
	saved, err := store.SaveGraphPositions(ctx, workspace.ID, []model.GraphPositionUpdate{{NodeID: nodeID, XPct: 37.6254, YPct: 14.0834}})
	if err != nil {
		t.Fatal(err)
	}
	if saved[0].XPct != 37.625 || saved[0].YPct != 14.083 {
		t.Fatalf("fractional position was not normalized to three decimals: %+v", saved[0])
	}
	_, err = store.SaveGraphPositions(ctx, workspace.ID, []model.GraphPositionUpdate{
		{NodeID: nodeID, XPct: 99.999, YPct: 1.111},
		{NodeID: nodeID + "-missing", XPct: 10, YPct: 10},
	})
	if err == nil {
		t.Fatal("mixed valid/invalid position batch should fail")
	}
	snapshot, err := store.BuildGraphSnapshot(ctx, library.ID, workspace.ID, "", 1, 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Position == nil || snapshot.Nodes[0].Position.XPct != 37.625 || snapshot.Nodes[0].Position.YPct != 14.083 {
		t.Fatalf("failed batch partially changed the saved position: %+v", snapshot.Nodes)
	}
	if _, err := store.SaveGraphPositions(ctx, workspace.ID, []model.GraphPositionUpdate{{NodeID: nodeID, XPct: math.NaN(), YPct: 50}}); err == nil {
		t.Fatal("NaN graph position should be rejected")
	}
}

func TestGraphSnapshotIncludesOnlyStableSameLibraryEvidence(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, _ := store.CreateLibrary(ctx, "Graph evidence", "")
	otherLibrary, _ := store.CreateLibrary(ctx, "Other graph", "")
	target := publishGraphKnowledge(t, store, library.ID, "graph-target", model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "Target knowledge", Description: "Stable target", Language: "en",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "Claim", Content: "This is a stable target."}},
	})
	objectPath, digest, err := store.PutObject(strings.NewReader("graph source payload"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := store.CreatePendingDocument(ctx, library.ID, "graph-source.md", "text/markdown", "graph-source.md", "", objectPath, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceChunks(ctx, document.ID, []model.Chunk{{ID: "graph-chunk", DocumentID: document.ID, Text: "graph source payload", Location: map[string]any{"line": 1}, ContentHash: digest}}); err != nil {
		t.Fatal(err)
	}
	source := publishGraphKnowledge(t, store, library.ID, "graph-source", model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "Source knowledge", Description: "Stable source", Language: "en",
		Sections:   []model.KnowledgeSection{{ID: "claim", Heading: "Claim", Content: "This cites a document.[^doc]"}},
		Sources:    []model.KnowledgeSource{{ID: "doc", Resource: DocumentURI(document.ID), Title: "Graph source", Snapshot: model.KnowledgeSourceSnapshot{Status: "verified", ContentHash: digest}}},
		Relations:  []model.KnowledgeRelation{{Type: "related", Target: target.URI}},
		Derivation: &model.KnowledgeDerivation{Premises: []string{DocumentURI(document.ID)}, Method: "evidence synthesis", Conclusion: "Stable source", Limitations: "Single source"},
	})
	_, _, err = store.CreateKnowledgeDraft(ctx, KnowledgeDraftInput{LibraryID: library.ID, ClientSubmissionID: "graph-draft", Mode: "create", Payload: model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "Draft must stay hidden", Description: "Draft", Language: "en",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "Claim", Content: "Unpublished."}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	foreign := publishGraphKnowledge(t, store, otherLibrary.ID, "graph-foreign", model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "Foreign knowledge", Description: "Other library", Language: "en",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "Claim", Content: "Must stay outside this graph."}},
	})

	snapshot, err := store.BuildGraphSnapshot(ctx, library.ID, "", "", 9, 999)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Nodes) != 3 || len(snapshot.Edges) != 3 {
		t.Fatalf("unexpected graph snapshot: nodes=%+v edges=%+v", snapshot.Nodes, snapshot.Edges)
	}
	if !slices.ContainsFunc(snapshot.Edges, func(edge model.GraphEdge) bool {
		return edge.Kind == "derivation" && edge.RelationType == "derived-from"
	}) {
		t.Fatalf("stable knowledge derivation is missing: %+v", snapshot.Edges)
	}
	if snapshot.Limits.RequestedDepth != 9 || snapshot.Limits.AppliedDepth != 2 || snapshot.Limits.MaxNodes != maximumGraphNodeLimit {
		t.Fatalf("graph limits were not bounded: %+v", snapshot.Limits)
	}
	for _, node := range snapshot.Nodes {
		if strings.Contains(node.Label, "Draft") || node.URI == foreign.URI {
			t.Fatalf("graph leaked a draft or another library: %+v", node)
		}
	}
	workspace := snapshot.Workspace
	foreignNodeID := graphKnowledgeNodeID(foreign.URI, foreign.Revision)
	if _, err := store.SaveGraphPositions(ctx, workspace.ID, []model.GraphPositionUpdate{{NodeID: foreignNodeID, XPct: 20.125, YPct: 30.5}}); !errors.Is(err, ErrGraphNodeNotFound) {
		t.Fatalf("cross-library graph node should be rejected, got %v", err)
	}
	sourceNodeID := graphKnowledgeNodeID(source.URI, source.Revision)
	if _, err := store.SaveGraphPositions(ctx, workspace.ID, []model.GraphPositionUpdate{{NodeID: sourceNodeID, XPct: -0.001, YPct: 50}}); err == nil {
		t.Fatal("out-of-range graph position should be rejected")
	}
}

func TestGraphSnapshotReportsTruncation(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, _ := store.CreateLibrary(ctx, "Graph truncation", "")
	for index, title := range []string{"Graph A", "Graph B"} {
		publishGraphKnowledge(t, store, library.ID, "truncate-"+title, model.KnowledgePayload{
			Schema: KAHKnowledgeSchema, Type: "claim", Title: title, Description: "Limit", Language: "en",
			Sections:        []model.KnowledgeSection{{ID: "claim", Heading: "Claim", Content: "Unique truncation content " + title}},
			DuplicateIntent: "independent",
		})
		_ = index
	}
	snapshot, err := store.BuildGraphSnapshot(ctx, library.ID, "", "", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Truncated || snapshot.HiddenNodes != 1 || len(snapshot.Nodes) != 1 {
		t.Fatalf("unexpected truncation metadata: %+v", snapshot)
	}
}

func TestGraphWorkspaceAndFractionalPositionSurviveBackupRestore(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, _ := store.CreateLibrary(ctx, "Graph backup", "")
	revision := publishGraphKnowledge(t, store, library.ID, "graph-backup", model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "Backed up graph", Description: "Layout survives", Language: "en",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "Claim", Content: "The graph layout is backup data."}},
	})
	workspace, err := store.CreateGraphWorkspace(ctx, library.ID, "Research map")
	if err != nil {
		t.Fatal(err)
	}
	nodeID := graphKnowledgeNodeID(revision.URI, revision.Revision)
	if _, err := store.SaveGraphPositions(ctx, workspace.ID, []model.GraphPositionUpdate{{NodeID: nodeID, XPct: 12.3456, YPct: 87.6544}}); err != nil {
		t.Fatal(err)
	}
	backupPath, _, err := store.CreateBackup(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored-graph")
	if _, err := store.RestoreBackupTo(ctx, backupPath, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	snapshot, err := restored.BuildGraphSnapshot(ctx, library.ID, workspace.ID, "", 1, 250)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Workspace.Name != "Research map" || len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Position == nil || snapshot.Nodes[0].Position.XPct != 12.346 || snapshot.Nodes[0].Position.YPct != 87.654 {
		t.Fatalf("restored graph layout mismatch: %+v", snapshot)
	}
}

func TestGraphFollowsStableRevisionWithoutMutatingOldLayout(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, _ := store.CreateLibrary(ctx, "Graph revisions", "")
	payload := model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "Revision one", Description: "First graph revision", Language: "en",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "Claim", Content: "The first stable graph revision."}},
	}
	first := publishGraphKnowledge(t, store, library.ID, "graph-revision-1", payload)
	workspace, _ := store.GetOrCreateDefaultGraphWorkspace(ctx, library.ID)
	firstNodeID := graphKnowledgeNodeID(first.URI, first.Revision)
	if _, err := store.SaveGraphPositions(ctx, workspace.ID, []model.GraphPositionUpdate{{NodeID: firstNodeID, XPct: 20.125, YPct: 30.875}}); err != nil {
		t.Fatal(err)
	}
	payload.Title = "Revision two"
	payload.Sections = []model.KnowledgeSection{{ID: "claim", Heading: "Claim", Content: "The second stable graph revision."}}
	submission, _, err := store.CreateKnowledgeDraft(ctx, KnowledgeDraftInput{LibraryID: library.ID, ClientSubmissionID: "graph-revision-2", Mode: "propose_revision", BaseURI: first.URI, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReviewKAHSubmission(ctx, submission.ID, "graph-test", "approve", ""); err != nil {
		t.Fatal(err)
	}
	second, err := store.PublishKAHSubmission(ctx, submission.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.BuildGraphSnapshot(ctx, library.ID, workspace.ID, "", 1, 250)
	if err != nil {
		t.Fatal(err)
	}
	secondNodeID := graphKnowledgeNodeID(second.URI, second.Revision)
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].ID != secondNodeID || snapshot.Nodes[0].Position != nil {
		t.Fatalf("graph did not advance cleanly to the new stable revision: %+v", snapshot.Nodes)
	}
	var retained int
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_node_positions WHERE workspace_id=? AND node_id=?`, workspace.ID, firstNodeID).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("old auditable layout was mutated or deleted: count=%d err=%v", retained, err)
	}
}

func TestGraphScaleFixturesRespectRenderCapAndLatency(t *testing.T) {
	for _, count := range []int{50, 250, 300, 1000} {
		t.Run(fmt.Sprintf("nodes_%d", count), func(t *testing.T) {
			store, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			library, _ := store.CreateLibrary(ctx, fmt.Sprintf("Scale %d", count), "")
			tx, err := store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			stamp := time.Now().UTC().Format(time.RFC3339Nano)
			for index := 0; index < count; index++ {
				id := fmt.Sprintf("scale-%04d", index)
				payload, _ := json.Marshal(model.KnowledgePayload{Schema: KAHKnowledgeSchema, Type: "claim", Title: fmt.Sprintf("Node %04d", index), Description: "Scale fixture", Language: "en", Tags: []string{}})
				if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_items(id,library_id,stable_revision,created_at,updated_at) VALUES(?,?,1,?,?)`, id, library.ID, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_revisions(knowledge_id,revision,payload_json,markdown,content_hash,status,flags_json,submitted_by,created_at) VALUES(?,1,?,'',?,'stable','[]','fixture',?)`, id, string(payload), fmt.Sprintf("hash-%04d", index), stamp); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			snapshot, err := store.BuildGraphSnapshot(ctx, library.ID, "", "", 1, 300)
			elapsed := time.Since(started)
			if err != nil {
				t.Fatal(err)
			}
			expected := count
			if expected > 300 {
				expected = 300
			}
			if len(snapshot.Nodes) != expected || snapshot.Truncated != (count > 300) || snapshot.HiddenNodes != count-expected {
				t.Fatalf("scale bounds mismatch: nodes=%d truncated=%t hidden=%d", len(snapshot.Nodes), snapshot.Truncated, snapshot.HiddenNodes)
			}
			if elapsed > time.Second {
				t.Fatalf("graph fixture exceeded 1s service target: %s", elapsed)
			}
			t.Logf("graph scale fixture: source=%d returned=%d elapsed=%s", count, len(snapshot.Nodes), elapsed)
		})
	}
}

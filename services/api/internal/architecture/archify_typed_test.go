package architecture

import (
	"bytes"
	"reflect"
	"sort"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestArchifySequenceParticipantLabelsKeepSourceSemanticsAndBoundWidth(t *testing.T) {
	nodes := []DiagramNode{
		{ID: "express", Label: "express@4.18.2"},
		{ID: "runtime", Label: "runtime configuration"},
		{ID: "api", Label: "Api/Program.cs"},
		{ID: "importer", Label: "scripts/importer.py"},
	}
	original := append([]DiagramNode(nil), nodes...)
	labels := archifySequenceParticipantLabels(nodes)

	want := map[string]string{
		"express":  "express",
		"runtime":  "runtime cfg",
		"api":      "Program.cs",
		"importer": "importer.py",
	}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("normalized participant labels = %#v, want %#v", labels, want)
	}
	if !reflect.DeepEqual(nodes, original) {
		t.Fatalf("label normalization mutated source diagram nodes: before=%#v after=%#v", original, nodes)
	}
	for id, label := range labels {
		if label == "" || archifyTextUnits(label) > archifySequenceParticipantMaxUnits {
			t.Fatalf("participant %q has invalid display label %q (%d units)", id, label, archifyTextUnits(label))
		}
	}
	if got := firstEvidence([]string{"Api/Program.cs"}); got != "Program.cs" {
		t.Fatalf("first evidence basename = %q, want Program.cs", got)
	}
	if got := firstEvidence([]string{"scripts/importer.py"}); got != "importer.py" {
		t.Fatalf("first evidence basename = %q, want importer.py", got)
	}
}

func TestArchifySequenceParticipantLabelsAreDeterministicAndCollisionFree(t *testing.T) {
	nodes := []DiagramNode{
		{ID: "node-c", Label: "shared-prefix-gamma"},
		{ID: "node-a", Label: "shared-prefix-alpha"},
		{ID: "node-b", Label: "shared-prefix-beta"},
		{ID: "node-d", Label: "shared-prefix-alpha"},
	}
	first := archifySequenceParticipantLabels(nodes)
	reordered := append([]DiagramNode(nil), nodes...)
	sort.Slice(reordered, func(i, j int) bool { return reordered[i].ID > reordered[j].ID })
	second := archifySequenceParticipantLabels(reordered)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("collision resolution depends on input order: first=%#v second=%#v", first, second)
	}

	seen := make(map[string]string, len(first))
	for id, label := range first {
		if previous, exists := seen[label]; exists {
			t.Fatalf("participant labels collide: %q and %q both use %q", previous, id, label)
		}
		seen[label] = id
		if archifyTextUnits(label) > archifySequenceParticipantMaxUnits {
			t.Fatalf("collision label %q is too wide: %d units", label, archifyTextUnits(label))
		}
	}
	if first["node-a"] != "shared-prefi…" || first["node-b"] != "shared-pre…#2" || first["node-c"] != "shared-pre…#3" || first["node-d"] != "shared-pre…#4" {
		t.Fatalf("unexpected stable collision suffixes: %#v", first)
	}
}

func TestArchifySequenceParticipantLabelsKeepArchifySourceInputAndRender(t *testing.T) {
	project := model.Project{ID: "long-label-project", Name: "长标签项目"}
	snapshot := model.ArchitectureSnapshot{
		ID: "long-label-snapshot", ProjectID: project.ID, ContentHash: "long-label-hash",
		Coverage: model.ArchitectureCoverage{IncludedFiles: 1, ByLanguage: map[string]int{"go": 1}},
		Modules:  []model.ArchitectureModule{{Name: "api", Language: "go", RootPath: ".", EvidencePaths: []string{"Api/Program.cs"}}},
	}
	analysis := model.ArchitectureAnalysis{
		SchemaVersion: "kah.architecture.analysis.v1", ProjectID: project.ID, SnapshotID: snapshot.ID,
		Facts: []model.AnalysisFact{
			{ID: "f-express", Subject: "express@4.18.2", Predicate: "uses", Object: "runtime configuration", Evidence: []string{"package.json"}},
			{ID: "f-api", Subject: "Api/Program.cs", Predicate: "loads", Object: "scripts/importer.py", Evidence: []string{"Api/Program.cs"}},
		},
	}
	ir := BuildArchitectureIR(project, snapshot, analysis)
	sequence, err := BuildArchifySequenceInput(project, snapshot, ir)
	if err != nil {
		t.Fatalf("build sequence input: %v", err)
	}
	if sequence.Meta.ColumnFit != "fixed" || sequence.Meta.ViewBox[0] < 920 {
		t.Fatalf("unexpected sequence layout: %#v", sequence.Meta)
	}

	sourceLabels := make(map[string]string, len(ir.Diagrams[2].Nodes))
	for _, node := range ir.Diagrams[2].Nodes {
		sourceLabels[node.ID] = node.Label
	}
	for _, participant := range sequence.Participants {
		if participant.ID == "participant-unknown-target" || participant.ID == "participant-unknown-source" {
			continue
		}
		var source string
		for id, label := range sourceLabels {
			if "participant-"+stableArchifyID(id) == participant.ID {
				source = label
				break
			}
		}
		if source == "" {
			t.Fatalf("participant %q has no source IR node", participant.ID)
		}
		if participant.Label == source {
			continue
		}
		if compactArchifyParticipantLabel(source) != participant.Label {
			t.Fatalf("participant %q label %q is not a bounded projection of source %q", participant.ID, participant.Label, source)
		}
	}

	cli := NewArchifyCLI("", "")
	if !cli.Available() {
		t.Fatalf("vendored Archify CLI is unavailable: root=%q node=%q", cli.Root, cli.Node)
	}
	result, err := cli.RenderArchitecture(t.Context(), project, snapshot, analysis, ir, workspaceArchifyRoot(t))
	if err != nil {
		t.Fatalf("vendored Archify rejected bounded participant labels: %v", err)
	}
	supporting, ok := result.Supporting["sequence"]
	if !ok {
		t.Fatal("vendored Archify result has no sequence artifact")
	}
	if accepted, _ := supporting.Validation["ok"].(bool); !accepted {
		t.Fatalf("vendored Archify sequence validation receipt: %#v", supporting.Validation)
	}
	if !bytes.Contains(supporting.SVG, []byte("express")) || !bytes.Contains(supporting.SVG, []byte("runtime cfg")) {
		t.Fatalf("sequence SVG does not contain bounded participant labels")
	}
}

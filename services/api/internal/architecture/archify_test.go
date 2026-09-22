package architecture

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func workspaceArchifyRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for current := cwd; ; current = filepath.Dir(current) {
		candidate := filepath.Join(current, ".run-data")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Join(candidate, "archify-test-work")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return t.TempDir()
		}
	}
}

func TestArchifyIRValidationAndOfflineExports(t *testing.T) {
	project := model.Project{ID: "project-1", Name: "示例项目"}
	snapshot := model.ArchitectureSnapshot{ID: "snapshot-1", ProjectID: project.ID, ContentHash: "hash", Coverage: model.ArchitectureCoverage{IncludedFiles: 2}, Modules: []model.ArchitectureModule{{Name: "api", Language: "go", RootPath: ".", Dependencies: []string{"sqlite"}, EvidencePaths: []string{"main.go"}}}}
	analysis := model.ArchitectureAnalysis{SchemaVersion: "kah.architecture.analysis.v1", ProjectID: project.ID, SnapshotID: snapshot.ID, Facts: []model.AnalysisFact{{ID: "f1", Subject: "api", Predicate: "stores", Object: "sqlite"}}}
	ir := BuildArchitectureIR(project, snapshot, analysis)
	if err := ValidateArchitectureIR(ir); err != nil {
		t.Fatal(err)
	}
	jsonData, err := RenderArchitectureJSON(ir)
	if err != nil || !json.Valid(jsonData) {
		t.Fatalf("json export: %v", err)
	}
	markdown, err := RenderArchitectureMarkdown(project, snapshot, analysis, ir)
	if err != nil || !bytes.Contains(markdown, []byte("示例项目")) {
		t.Fatalf("markdown export: %v", err)
	}
	html, err := RenderArchitectureHTML(ir)
	if err != nil || !bytes.Contains(html, []byte("<!doctype html>")) || !bytes.Contains(html, []byte("kah.architecture.diagram.v1")) {
		t.Fatalf("html export: %v", err)
	}
	svg, err := RenderArchitectureSVG(ir)
	if err != nil || !bytes.HasPrefix(svg, []byte("<svg")) {
		t.Fatalf("svg export: %v", err)
	}
	png, err := RenderArchitecturePNG(ir)
	if err != nil || !bytes.HasPrefix(png, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("png export: %v", err)
	}
}

func TestArchifyRejectsUnknownMetadataAndKeepsUnknownDeploymentExplicit(t *testing.T) {
	ir := BuildArchitectureIR(model.Project{ID: "p", Name: "p"}, model.ArchitectureSnapshot{ID: "s", ProjectID: "p", Modules: []model.ArchitectureModule{{Name: "x", RootPath: ".", Unknown: true}}}, model.ArchitectureAnalysis{})
	if len(ir.UnknownNotes) != 1 || ir.UnknownNotes[0] == "" {
		t.Fatalf("unknown deployment was not recorded: %+v", ir.UnknownNotes)
	}
	ir.Renderer = "other"
	if err := ValidateArchitectureIR(ir); err == nil {
		t.Fatal("invalid renderer metadata was accepted")
	}
	ir = BuildArchitectureIR(model.Project{ID: "p", Name: "p"}, model.ArchitectureSnapshot{ID: "s", ProjectID: "p"}, model.ArchitectureAnalysis{})
	ir.Diagrams[0].Edges = append(ir.Diagrams[0].Edges, DiagramEdge{From: "missing", To: "missing", Relation: "unknown"})
	if err := ValidateArchitectureIR(ir); err == nil {
		t.Fatal("diagram edge outside node set was accepted")
	}
	ir = BuildArchitectureIR(model.Project{ID: "p", Name: "p"}, model.ArchitectureSnapshot{ID: "s", ProjectID: "p"}, model.ArchitectureAnalysis{})
	ir.Diagrams[0].Kind = "modules"
	if err := ValidateArchitectureIR(ir); err == nil {
		t.Fatal("diagram kind/id mismatch was accepted")
	}
}

func TestArchifyVendoredCLIProducesValidatedArtifacts(t *testing.T) {
	cli := NewArchifyCLI("", "")
	if !cli.Available() {
		t.Fatalf("vendored Archify CLI is unavailable: root=%q node=%q", cli.Root, cli.Node)
	}
	project := model.Project{ID: "project-cli", Name: "CLI 项目"}
	snapshot := model.ArchitectureSnapshot{
		ID: "snapshot-cli", ProjectID: project.ID, ContentHash: "hash-cli",
		Coverage: model.ArchitectureCoverage{IncludedFiles: 1, ByLanguage: map[string]int{"go": 1}},
		Modules:  []model.ArchitectureModule{{Name: "api", Language: "go", RootPath: ".", EvidencePaths: []string{"main.go"}}},
	}
	analysis := model.ArchitectureAnalysis{SchemaVersion: "kah.architecture.analysis.v1", ProjectID: project.ID, SnapshotID: snapshot.ID}
	ir := BuildArchitectureIR(project, snapshot, analysis)
	result, err := cli.RenderArchitecture(t.Context(), project, snapshot, analysis, ir, workspaceArchifyRoot(t))
	if err != nil {
		t.Fatalf("vendored Archify render: %v", err)
	}
	if ok, _ := result.Validation["ok"].(bool); !ok {
		t.Fatalf("Archify validation receipt: %#v", result.Validation)
	}
	if ok, _ := result.Delivery["ok"].(bool); !ok {
		t.Fatalf("Archify delivery receipt: %#v", result.Delivery)
	}
	if !bytes.Contains(result.HTML, []byte("<svg")) || !bytes.Contains(result.HTML, []byte("api")) {
		t.Fatalf("Archify HTML does not contain rendered evidence: %d bytes", len(result.HTML))
	}
	if !bytes.HasPrefix(result.SVG, []byte("<svg")) || !bytes.HasPrefix(result.PNG, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("Archify exports are not valid HTML/SVG/PNG")
	}
	if result.PNGRenderer == "" {
		t.Fatal("PNG renderer health is missing")
	}
	for _, kind := range []string{"sequence", "dataflow"} {
		supporting, ok := result.Supporting[kind]
		if !ok {
			t.Fatalf("missing supporting Archify output %q", kind)
		}
		if accepted, _ := supporting.Validation["ok"].(bool); !accepted {
			t.Fatalf("%s validation receipt: %#v", kind, supporting.Validation)
		}
		if accepted, _ := supporting.Delivery["ok"].(bool); !accepted {
			t.Fatalf("%s delivery receipt: %#v", kind, supporting.Delivery)
		}
		if !bytes.HasPrefix(supporting.SVG, []byte("<svg")) || !bytes.Contains(supporting.HTML, []byte("<svg")) {
			t.Fatalf("%s Archify output is not a self-contained SVG document", kind)
		}
	}
	if strings.HasPrefix(result.PNGRenderer, "kah-") && result.BrowserError == "" {
		t.Fatal("fallback PNG must expose browser health error")
	}
}

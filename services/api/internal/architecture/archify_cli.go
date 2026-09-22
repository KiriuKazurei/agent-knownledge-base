package architecture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

var (
	ErrArchifyUnavailable        = errors.New("archify Node CLI is unavailable")
	ErrArchifyBrowserUnavailable = errors.New("archify browser PNG export is unavailable")
)

// ArchifyCLI is the fixed, local copy of the upstream renderer. Root must
// point at the vendored package and is never accepted from project input.
// Node is kept configurable for packaged installations while the command
// arguments remain a fixed allow-list in renderArchitecture.
type ArchifyCLI struct {
	Root string
	Node string
	// WorkRoot is an optional writable staging directory for the CLI. It is
	// kept separate from the source project and final artifact data root.
	WorkRoot string
	Timeout  time.Duration
}

type ArchifyArtifacts struct {
	HTML         []byte
	SVG          []byte
	PNG          []byte
	Validation   map[string]any
	Delivery     map[string]any
	PNGRenderer  string
	BrowserError string
	// Supporting contains the real Archify receipts and rendered output for
	// the sequence and dataflow views. The HTTP artifact contract keeps the
	// overview HTML/SVG as the primary export while the KAH diagram JSON
	// retains all four views and this field proves each supporting view passed
	// the same fixed renderer pipeline.
	Supporting map[string]ArchifyDiagramArtifact
}

type ArchifyDiagramArtifact struct {
	HTML       []byte
	SVG        []byte
	Validation map[string]any
	Delivery   map[string]any
}

type archifyMeta struct {
	Title          string `json:"title"`
	Locale         string `json:"locale,omitempty"`
	Subtitle       string `json:"subtitle,omitempty"`
	QualityProfile string `json:"quality_profile,omitempty"`
}

type archifyComponent struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	Label    string     `json:"label"`
	Sublabel string     `json:"sublabel,omitempty"`
	Tag      string     `json:"tag,omitempty"`
	Pos      [2]float64 `json:"pos,omitempty"`
	Size     [2]float64 `json:"size,omitempty"`
}

type archifyConnection struct {
	ID       string `json:"id,omitempty"`
	From     string `json:"from"`
	To       string `json:"to"`
	Label    string `json:"label,omitempty"`
	Variant  string `json:"variant,omitempty"`
	FromSide string `json:"fromSide,omitempty"`
	ToSide   string `json:"toSide,omitempty"`
}

type archifyCard struct {
	Dot   string   `json:"dot"`
	Title string   `json:"title"`
	Items []string `json:"items"`
}

// ArchifyArchitectureInput is the upstream architecture.schema.json
// document. It deliberately does not expose the internal KAH IR fields to
// the external renderer.
type ArchifyArchitectureInput struct {
	SchemaVersion int                 `json:"schema_version"`
	DiagramType   string              `json:"diagram_type"`
	Meta          archifyMeta         `json:"meta"`
	Components    []archifyComponent  `json:"components"`
	Connections   []archifyConnection `json:"connections,omitempty"`
	Cards         []archifyCard       `json:"cards,omitempty"`
}

// DiscoverArchifyRoot finds only a repository-owned vendored package. It
// walks a bounded set of ancestors so a server started from services/api,
// the repository root, or a packaged executable can use the same resource.
func DiscoverArchifyRoot() string {
	starts := []string{}
	if configured := strings.TrimSpace(os.Getenv("KAH_ARCHIFY_ROOT")); configured != "" {
		starts = append(starts, configured)
	}
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if executable, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(executable))
	}
	for _, start := range starts {
		current, err := filepath.Abs(start)
		if err != nil {
			continue
		}
		for depth := 0; depth < 8; depth++ {
			candidate := filepath.Join(current, "third_party", "archify", "upstream")
			if archifyRootLooksValid(candidate) {
				return candidate
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}
	return ""
}

func archifyRootLooksValid(root string) bool {
	for _, relative := range []string{"bin/archify.mjs", "schemas/architecture.schema.json", "assets/template.html"} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func NewArchifyCLI(root, node string) ArchifyCLI {
	if strings.TrimSpace(root) == "" {
		root = DiscoverArchifyRoot()
	}
	if strings.TrimSpace(node) == "" {
		node = strings.TrimSpace(os.Getenv("KAH_NODE_PATH"))
	}
	if node == "" {
		node = "node"
	}
	return ArchifyCLI{Root: root, Node: node, Timeout: 2 * time.Minute}
}

func (c ArchifyCLI) Available() bool {
	if !archifyRootLooksValid(c.Root) {
		return false
	}
	_, err := exec.LookPath(c.Node)
	return err == nil
}

func (c ArchifyCLI) RenderArchitecture(ctx context.Context, project model.Project, snapshot model.ArchitectureSnapshot, analysis model.ArchitectureAnalysis, ir ArchitectureIR, outputRoot string) (ArchifyArtifacts, error) {
	if !c.Available() {
		return ArchifyArtifacts{}, ErrArchifyUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateArchitectureIR(ir); err != nil {
		return ArchifyArtifacts{}, err
	}
	spec, err := BuildArchifyArchitectureInput(project, snapshot, analysis, ir)
	if err != nil {
		return ArchifyArtifacts{}, err
	}
	specBytes, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return ArchifyArtifacts{}, err
	}
	if strings.TrimSpace(c.WorkRoot) != "" {
		outputRoot = c.WorkRoot
	}
	if outputRoot == "" {
		outputRoot = os.TempDir()
	}
	if err := os.MkdirAll(outputRoot, 0o750); err != nil {
		return ArchifyArtifacts{}, err
	}
	working, err := os.MkdirTemp(outputRoot, ".archify-render-")
	if err != nil {
		return ArchifyArtifacts{}, err
	}
	defer os.RemoveAll(working)
	primary, err := c.renderTyped(ctx, working, "architecture", "architecture", specBytes)
	if err != nil {
		return ArchifyArtifacts{}, err
	}
	// The overview and module relation views share Archify's architecture
	// schema. Sequence and dataflow use their own upstream typed schemas;
	// render both so the generated package cannot silently claim the KAH IR is
	// only an architecture graph.
	supporting := make(map[string]ArchifyDiagramArtifact, 2)
	sequence, err := BuildArchifySequenceInput(project, snapshot, ir)
	if err != nil {
		return ArchifyArtifacts{}, err
	}
	sequenceBytes, err := json.MarshalIndent(sequence, "", "  ")
	if err != nil {
		return ArchifyArtifacts{}, err
	}
	sequenceArtifact, err := c.renderTyped(ctx, working, "sequence", "sequence", sequenceBytes)
	if err != nil {
		return ArchifyArtifacts{}, err
	}
	supporting["sequence"] = sequenceArtifact
	dataflow, err := BuildArchifyDataflowInput(project, snapshot, ir)
	if err != nil {
		return ArchifyArtifacts{}, err
	}
	dataflowBytes, err := json.MarshalIndent(dataflow, "", "  ")
	if err != nil {
		return ArchifyArtifacts{}, err
	}
	dataflowArtifact, err := c.renderTyped(ctx, working, "dataflow", "dataflow", dataflowBytes)
	if err != nil {
		return ArchifyArtifacts{}, err
	}
	supporting["dataflow"] = dataflowArtifact
	png, renderer, browserError := c.exportPNG(ctx, filepath.Join(working, "architecture.html"), working, ir)
	return ArchifyArtifacts{
		HTML: primary.HTML, SVG: primary.SVG, PNG: png,
		Validation: primary.Validation, Delivery: primary.Delivery,
		PNGRenderer: renderer, BrowserError: browserError,
		Supporting: supporting,
	}, nil
}

func (c ArchifyCLI) renderTyped(ctx context.Context, working, diagramType, fileStem string, specBytes []byte) (ArchifyDiagramArtifact, error) {
	inputPath := filepath.Join(working, fileStem+".json")
	renderPath := filepath.Join(working, fileStem+".render.html")
	deliveryPath := filepath.Join(working, fileStem+".html")
	if err := os.WriteFile(inputPath, specBytes, 0o640); err != nil {
		return ArchifyDiagramArtifact{}, err
	}
	validationOutput, err := c.run(ctx, working, "validate", diagramType, inputPath, "--json", "--quality", "standard")
	if err != nil {
		return ArchifyDiagramArtifact{}, fmt.Errorf("archify %s validate: %w", diagramType, err)
	}
	validation := decodeArchifyReceipt(validationOutput)
	if ok, _ := validation["ok"].(bool); !ok {
		return ArchifyDiagramArtifact{}, fmt.Errorf("archify %s validation failed: %s", diagramType, summarizeArchifyOutput(validationOutput))
	}
	if _, err := c.run(ctx, working, "render", diagramType, inputPath, renderPath, "--quality", "standard"); err != nil {
		return ArchifyDiagramArtifact{}, fmt.Errorf("archify %s render: %w", diagramType, err)
	}
	deliveryOutput, err := c.run(ctx, working, "deliver", diagramType, inputPath, deliveryPath, "--json", "--quality", "standard")
	if err != nil {
		return ArchifyDiagramArtifact{}, fmt.Errorf("archify %s deliver: %w", diagramType, err)
	}
	delivery := decodeArchifyReceipt(deliveryOutput)
	if ok, _ := delivery["ok"].(bool); !ok {
		return ArchifyDiagramArtifact{}, fmt.Errorf("archify %s delivery failed: %s", diagramType, summarizeArchifyOutput(deliveryOutput))
	}
	html, err := os.ReadFile(deliveryPath)
	if err != nil {
		return ArchifyDiagramArtifact{}, err
	}
	svg, err := extractArchifySVG(html)
	if err != nil {
		return ArchifyDiagramArtifact{}, fmt.Errorf("archify %s SVG: %w", diagramType, err)
	}
	return ArchifyDiagramArtifact{HTML: html, SVG: svg, Validation: validation, Delivery: delivery}, nil
}

func (c ArchifyCLI) run(parent context.Context, working string, args ...string) ([]byte, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, c.Node, append([]string{filepath.Join(c.Root, "bin", "archify.mjs")}, args...)...)
	command.Dir = c.Root
	temp := filepath.Join(working, "tmp")
	if err := os.MkdirAll(temp, 0o750); err != nil {
		return nil, err
	}
	// Windows keeps the first value when a process receives duplicate
	// environment keys. Filter inherited temp keys before appending the
	// task-local directory; otherwise Archify's child renderer may resolve the
	// host profile temp path and fail before it can write a receipt.
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		switch strings.ToUpper(key) {
		case "TEMP", "TMP", "TMPDIR", "ARCHIFY_UPDATE_CHECK_DISABLED":
			continue
		default:
			env = append(env, entry)
		}
	}
	command.Env = append(env,
		"ARCHIFY_UPDATE_CHECK_DISABLED=1",
		"TEMP="+temp,
		"TMP="+temp,
		"TMPDIR="+temp,
	)
	// A packaged Electron build may not ship a system Node executable. The
	// desktop main process explicitly opts into Electron's documented
	// ELECTRON_RUN_AS_NODE mode when it points KAH_NODE_PATH at its own
	// executable. Keep that opt-in explicit; arbitrary project input can never
	// turn an executable into an Electron-as-Node process.
	if strings.TrimSpace(os.Getenv("KAH_ARCHIFY_ELECTRON_NODE")) == "1" {
		command.Env = append(command.Env, "ELECTRON_RUN_AS_NODE=1")
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if len(message) > 4096 {
			message = message[:4096]
		}
		return nil, fmt.Errorf("%w: %s", err, message)
	}
	return stdout.Bytes(), nil
}

func decodeArchifyReceipt(output []byte) map[string]any {
	var receipt map[string]any
	if err := json.Unmarshal(output, &receipt); err != nil {
		return map[string]any{}
	}
	return receipt
}

func summarizeArchifyOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	if len(text) > 4096 {
		return text[:4096]
	}
	return text
}

var archifySVGPattern = regexp.MustCompile(`(?s)<svg\b[^>]*>.*?</svg>`)

func extractArchifySVG(html []byte) ([]byte, error) {
	matches := archifySVGPattern.FindAll(html, -1)
	if len(matches) != 1 {
		return nil, fmt.Errorf("archify delivery must contain exactly one SVG, got %d", len(matches))
	}
	return append([]byte(nil), matches[0]...), nil
}

func (c ArchifyCLI) exportPNG(ctx context.Context, htmlPath, working string, ir ArchitectureIR) ([]byte, string, string) {
	visualOutput, err := c.run(ctx, working, "visual-check", htmlPath, "--json")
	if err == nil {
		receipt := decodeArchifyReceipt(visualOutput)
		if captures, ok := receipt["captures"].(map[string]any); ok {
			if screenshots, ok := captures["screenshots"].([]any); ok {
				for _, candidate := range screenshots {
					entry, ok := candidate.(map[string]any)
					if !ok {
						continue
					}
					pathValue, _ := entry["path"].(string)
					if pathValue == "" {
						continue
					}
					if data, readErr := os.ReadFile(pathValue); readErr == nil && len(data) > 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
						return data, "archify-visual-check", ""
					}
				}
			}
		}
	}
	// A browser is an optional runtime dependency. Keep the HTML/SVG path
	// fully real and expose the browser failure to callers while providing a
	// deterministic local PNG for installations without Chrome/Chromium.
	fallback, fallbackErr := RenderArchitecturePNG(ir)
	if fallbackErr != nil {
		return nil, "", fallbackErr.Error()
	}
	browserError := ErrArchifyBrowserUnavailable.Error()
	if err != nil {
		browserError = err.Error()
	}
	return fallback, "kah-offline-svg-fallback", browserError
}

func BuildArchifyArchitectureInput(project model.Project, snapshot model.ArchitectureSnapshot, analysis model.ArchitectureAnalysis, ir ArchitectureIR) (ArchifyArchitectureInput, error) {
	if err := ValidateArchitectureIR(ir); err != nil {
		return ArchifyArchitectureInput{}, err
	}
	diagram := ir.Diagrams[0]
	components := make([]archifyComponent, 0, len(diagram.Nodes))
	idMap := make(map[string]string, len(diagram.Nodes))
	for index, node := range diagram.Nodes {
		id := "node-" + stableArchifyID(node.ID)
		if _, exists := idMap[node.ID]; exists {
			id = fmt.Sprintf("%s-%d", id, index)
		}
		idMap[node.ID] = id
		row, col := index/4, index%4
		typeName := archifyComponentType(node.Kind)
		sublabel := ""
		if len(node.Evidence) > 0 {
			sublabel = "证据：" + node.Evidence[0]
		}
		components = append(components, archifyComponent{ID: id, Type: typeName, Label: node.Label, Sublabel: sublabel, Pos: [2]float64{80 + float64(col)*280, 100 + float64(row)*130}, Size: [2]float64{190, 68}})
	}
	if len(components) == 0 {
		components = append(components, archifyComponent{ID: "project", Type: "external", Label: project.Name, Sublabel: "未发现可绘制模块", Pos: [2]float64{80, 100}, Size: [2]float64{190, 68}})
	}
	connections := make([]archifyConnection, 0, len(diagram.Edges))
	for index, edge := range diagram.Edges {
		from, fromOK := idMap[edge.From]
		to, toOK := idMap[edge.To]
		if !fromOK || !toOK {
			continue
		}
		label := edge.Label
		if label == "" {
			label = edge.Relation
		}
		if edge.Unknown {
			label = "UNKNOWN: " + label
		}
		variant := "default"
		if edge.Unknown {
			variant = "dashed"
		}
		connections = append(connections, archifyConnection{ID: fmt.Sprintf("relation-%d-%s", index, stableArchifyID(edge.From+"-"+edge.To)), From: from, To: to, Label: label, Variant: variant})
	}
	if analysis.SchemaVersion == "" {
		analysis.SchemaVersion = "kah.architecture.analysis.v1"
	}
	title := strings.TrimSpace(project.Name)
	if title == "" {
		title = "项目技术架构"
	}
	subtitle := "快照 " + snapshot.ID
	if snapshot.ContentHash != "" {
		subtitle += " · " + snapshot.ContentHash
	}
	items := []string{fmt.Sprintf("扫描文件 %d，识别模块 %d", snapshot.Coverage.IncludedFiles, len(snapshot.Modules))}
	if snapshot.Coverage.UnknownFiles > 0 {
		items = append(items, fmt.Sprintf("未知文件 %d，关系保持 unknown", snapshot.Coverage.UnknownFiles))
	}
	return ArchifyArchitectureInput{
		SchemaVersion: 1,
		DiagramType:   "architecture",
		Meta:          archifyMeta{Title: title + " 技术架构", Locale: "zh-CN", Subtitle: subtitle, QualityProfile: "standard"},
		Components:    components,
		Connections:   connections,
		Cards:         []archifyCard{{Dot: "cyan", Title: "代码证据范围", Items: items}},
	}, nil
}

func stableArchifyID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])[:12]
}

func archifyComponentType(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "database", "db", "sqlite", "postgres", "mysql", "redis":
		return "database"
	case "cloud", "container", "kubernetes":
		return "cloud"
	case "security", "auth":
		return "security"
	case "external", "dependency", "unknown":
		return "external"
	default:
		return "backend"
	}
}

func (c ArchifyCLI) String() string {
	if runtime.GOOS == "windows" && strings.HasSuffix(strings.ToLower(c.Node), ".exe") {
		return fmt.Sprintf("archify@%s via %s", c.Root, c.Node)
	}
	return fmt.Sprintf("archify@%s via %s", c.Root, c.Node)
}

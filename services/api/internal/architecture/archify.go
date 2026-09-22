package architecture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sort"
	"strings"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

const (
	ArchifyCommit   = "72c750bb070d95171dbb2244e5b62b1b7da69c12"
	ArchifySource   = "https://github.com/tt-a1i/archify"
	ArchifyRenderer = "kah-archify-adapter-v1"
)

type DiagramNode struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Evidence []string `json:"evidence,omitempty"`
}

type DiagramEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Label    string `json:"label,omitempty"`
	Relation string `json:"relation"`
	Unknown  bool   `json:"unknown,omitempty"`
}

type ArchitectureDiagram struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Kind        string        `json:"kind"`
	Nodes       []DiagramNode `json:"nodes"`
	Edges       []DiagramEdge `json:"edges"`
	Description string        `json:"description,omitempty"`
}

type ArchitectureIR struct {
	SchemaVersion string                `json:"schemaVersion"`
	Renderer      string                `json:"renderer"`
	Upstream      string                `json:"upstream"`
	ProjectID     string                `json:"projectId"`
	SnapshotID    string                `json:"snapshotId"`
	Diagrams      []ArchitectureDiagram `json:"diagrams"`
	UnknownNotes  []string              `json:"unknownNotes,omitempty"`
}

func BuildArchitectureIR(project model.Project, snapshot model.ArchitectureSnapshot, analysis model.ArchitectureAnalysis) ArchitectureIR {
	ir := ArchitectureIR{SchemaVersion: "kah.architecture.diagram.v1", Renderer: ArchifyRenderer, Upstream: ArchifySource + "@" + ArchifyCommit, ProjectID: project.ID, SnapshotID: snapshot.ID, Diagrams: []ArchitectureDiagram{}}
	modules := append([]model.ArchitectureModule(nil), snapshot.Modules...)
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].RootPath+"\x00"+modules[i].Name < modules[j].RootPath+"\x00"+modules[j].Name
	})
	moduleNodes := make([]DiagramNode, 0, len(modules))
	moduleEdges := []DiagramEdge{}
	for _, module := range modules {
		id := "module:" + module.RootPath + ":" + module.Name
		moduleNodes = append(moduleNodes, DiagramNode{ID: id, Label: module.Name, Kind: languageKind(module.Language), Evidence: append([]string(nil), module.EvidencePaths...)})
		for _, dependency := range module.Dependencies {
			dependencyID := "dependency:" + dependency
			moduleNodes = appendUniqueNode(moduleNodes, DiagramNode{ID: dependencyID, Label: dependency, Kind: "dependency"})
			moduleEdges = append(moduleEdges, DiagramEdge{From: id, To: dependencyID, Label: dependency, Relation: "depends_on", Unknown: module.Unknown})
		}
	}
	ir.Diagrams = append(ir.Diagrams,
		ArchitectureDiagram{ID: "system-overview", Title: "系统总览", Kind: "overview", Nodes: moduleNodes, Edges: moduleEdges, Description: "由扫描快照和已验证分析生成；无法由证据确定的部署关系标记为 unknown。"},
		ArchitectureDiagram{ID: "module-relations", Title: "模块关系", Kind: "modules", Nodes: moduleNodes, Edges: moduleEdges},
	)
	sequenceNodes := []DiagramNode{}
	sequenceEdges := []DiagramEdge{}
	for _, fact := range analysis.Facts {
		from := "fact:" + fact.Subject
		to := "fact:" + fact.Object
		sequenceNodes = appendUniqueNode(sequenceNodes, DiagramNode{ID: from, Label: fact.Subject, Kind: "fact"})
		sequenceNodes = appendUniqueNode(sequenceNodes, DiagramNode{ID: to, Label: fact.Object, Kind: "fact"})
		sequenceEdges = append(sequenceEdges, DiagramEdge{From: from, To: to, Label: fact.Predicate, Relation: "fact"})
	}
	ir.Diagrams = append(ir.Diagrams,
		ArchitectureDiagram{ID: "key-sequence", Title: "关键时序", Kind: "sequence", Nodes: sequenceNodes, Edges: sequenceEdges, Description: "仅呈现分析输出声明的关键关系，未声明的调用顺序保持未知。"},
		ArchitectureDiagram{ID: "data-flow", Title: "数据流", Kind: "data-flow", Nodes: sequenceNodes, Edges: sequenceEdges},
	)
	for _, module := range modules {
		if module.Unknown {
			ir.UnknownNotes = append(ir.UnknownNotes, fmt.Sprintf("module %s (%s) has unknown deployment or dependency relations", module.Name, module.RootPath))
		}
	}
	sort.Slice(ir.UnknownNotes, func(i, j int) bool { return ir.UnknownNotes[i] < ir.UnknownNotes[j] })
	return ir
}

func appendUniqueNode(nodes []DiagramNode, candidate DiagramNode) []DiagramNode {
	for _, node := range nodes {
		if node.ID == candidate.ID {
			return nodes
		}
	}
	return append(nodes, candidate)
}

func languageKind(language string) string {
	if strings.TrimSpace(language) == "" {
		return "unknown"
	}
	return strings.ToLower(language)
}

func ValidateArchitectureIR(ir ArchitectureIR) error {
	if ir.SchemaVersion != "kah.architecture.diagram.v1" || ir.Renderer != ArchifyRenderer || ir.Upstream != ArchifySource+"@"+ArchifyCommit {
		return errors.New("invalid architecture diagram metadata")
	}
	if ir.ProjectID == "" || ir.SnapshotID == "" || len(ir.Diagrams) != 4 {
		return errors.New("architecture diagram IR must contain four diagrams")
	}
	seen := map[string]bool{}
	expectedKinds := map[string]string{"system-overview": "overview", "module-relations": "modules", "key-sequence": "sequence", "data-flow": "data-flow"}
	for _, diagram := range ir.Diagrams {
		if diagram.ID == "" || diagram.Title == "" || seen[diagram.ID] || expectedKinds[diagram.ID] != diagram.Kind {
			return errors.New("architecture diagram IDs must be unique")
		}
		seen[diagram.ID] = true
		nodes := map[string]bool{}
		for _, node := range diagram.Nodes {
			if node.ID == "" || nodes[node.ID] {
				return errors.New("architecture diagram node IDs must be unique")
			}
			nodes[node.ID] = true
		}
		for _, edge := range diagram.Edges {
			if edge.From == "" || edge.To == "" || edge.Relation == "" || !nodes[edge.From] || !nodes[edge.To] {
				return errors.New("architecture diagram edges require from, to, and relation")
			}
		}
	}
	return nil
}

func RenderArchitectureJSON(ir ArchitectureIR) ([]byte, error) {
	if err := ValidateArchitectureIR(ir); err != nil {
		return nil, err
	}
	return json.MarshalIndent(ir, "", "  ")
}

func RenderArchitectureMarkdown(project model.Project, snapshot model.ArchitectureSnapshot, analysis model.ArchitectureAnalysis, ir ArchitectureIR) ([]byte, error) {
	if err := ValidateArchitectureIR(ir); err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s 技术架构\n\n", project.Name)
	fmt.Fprintf(&b, "- 快照：`%s`\n- 内容哈希：`%s`\n- 绘图引擎：`%s`\n\n", snapshot.ID, snapshot.ContentHash, ir.Upstream)
	b.WriteString("## 技术栈\n\n")
	if len(snapshot.Coverage.ByLanguage) == 0 {
		b.WriteString("未识别到可归类的语言；覆盖范围见下方限制。\n\n")
	} else {
		languages := make([]string, 0, len(snapshot.Coverage.ByLanguage))
		for language := range snapshot.Coverage.ByLanguage {
			languages = append(languages, language)
		}
		sort.Strings(languages)
		for _, language := range languages {
			fmt.Fprintf(&b, "- `%s`：%d 个文件\n", language, snapshot.Coverage.ByLanguage[language])
		}
		b.WriteString("\n")
	}
	b.WriteString("## 目录职责与模块边界\n\n")
	for _, module := range snapshot.Modules {
		fmt.Fprintf(&b, "- **%s**（%s）`%s`：边界由清单 `%s` 定义", module.Name, module.Language, module.RootPath, strings.Join(module.DependencyFiles, "`, `"))
		if module.Version != "" {
			fmt.Fprintf(&b, "，版本 `%s`", module.Version)
		}
		if len(module.EntryPoints) > 0 {
			fmt.Fprintf(&b, "；入口：`%s`", strings.Join(module.EntryPoints, "`, `"))
		}
		if len(module.Dependencies) > 0 {
			fmt.Fprintf(&b, "；依赖：`%s`", strings.Join(module.Dependencies, "`, `"))
		}
		if module.Unknown {
			b.WriteString("，关系未知")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## 关键调用与数据流\n\n")
	if len(analysis.Facts) == 0 {
		b.WriteString("没有经验证据支持的调用或数据流关系；未知关系保持未知。\n")
	} else {
		for _, fact := range analysis.Facts {
			fmt.Fprintf(&b, "- FACT `%s`：`%s` —%s→ `%s`（证据：%s）\n", fact.ID, fact.Subject, fact.Predicate, fact.Object, strings.Join(fact.Evidence, ", "))
		}
	}
	b.WriteString("\n## 存储与外部接口\n\n")
	storageFacts, interfaceFacts := 0, 0
	for _, fact := range analysis.Facts {
		lower := strings.ToLower(fact.Predicate + " " + fact.Object)
		if strings.Contains(lower, "store") || strings.Contains(lower, "database") || strings.Contains(lower, "sqlite") || strings.Contains(lower, "postgres") {
			storageFacts++
			fmt.Fprintf(&b, "- 存储 FACT `%s`：`%s` —%s→ `%s`（证据：%s）\n", fact.ID, fact.Subject, fact.Predicate, fact.Object, strings.Join(fact.Evidence, ", "))
		}
		if strings.Contains(lower, "api") || strings.Contains(lower, "http") || strings.Contains(lower, "grpc") || strings.Contains(lower, "external") {
			interfaceFacts++
			fmt.Fprintf(&b, "- 接口 FACT `%s`：`%s` —%s→ `%s`（证据：%s）\n", fact.ID, fact.Subject, fact.Predicate, fact.Object, strings.Join(fact.Evidence, ", "))
		}
	}
	if storageFacts == 0 {
		b.WriteString("未发现有证据支持的存储关系。\n")
	}
	if interfaceFacts == 0 {
		b.WriteString("未发现有证据支持的外部接口关系。\n")
	}
	b.WriteString("\n## 构建与部署\n\n")
	for _, module := range snapshot.Modules {
		if len(module.ContainerFiles) > 0 {
			fmt.Fprintf(&b, "- 模块 `%s` 的容器/构建配置：`%s`\n", module.Name, strings.Join(module.ContainerFiles, "`, `"))
		}
	}
	b.WriteString("没有证据的部署关系不作结论。\n\n")
	b.WriteString("## 代码证据\n\n")
	fmt.Fprintf(&b, "扫描覆盖 %d 个文件，识别 %d 个模块，未知文件 %d 个。\n\n", snapshot.Coverage.IncludedFiles, len(snapshot.Modules), snapshot.Coverage.UnknownFiles)
	for _, evidence := range analysis.Evidence {
		fmt.Fprintf(&b, "- `%s`：`%s:%d-%d`，内容哈希 `%s`，类型 `%s`\n", evidence.ID, evidence.Path, evidence.LineStart, evidence.LineEnd, evidence.ContentHash, evidence.Kind)
	}
	b.WriteString("\n## 已知限制\n\n")
	fmt.Fprintf(&b, "- 跳过文件：%d；秘密文件：%d；二进制文件：%d；越界链接：%d。\n", snapshot.Coverage.IgnoredFiles, snapshot.Coverage.SecretFiles, snapshot.Coverage.BinaryFiles, snapshot.Coverage.OutsideLinks)
	for _, note := range ir.UnknownNotes {
		fmt.Fprintf(&b, "- UNKNOWN: %s\n", note)
	}
	if len(analysis.Facts) > 0 {
		b.WriteString("\n## 事实与推断\n\n")
	}
	for _, fact := range analysis.Facts {
		fmt.Fprintf(&b, "- FACT `%s`：%s %s（证据：%s）\n", fact.ID, fact.Subject, fact.Object, strings.Join(fact.Evidence, ", "))
	}
	if len(analysis.Inferences) > 0 {
		b.WriteString("\n### 推断（明确标注）\n\n")
	}
	for _, inference := range analysis.Inferences {
		fmt.Fprintf(&b, "- INFERENCE `%s`：%s（置信度 %.2f；证据：%s）\n", inference.ID, inference.Statement, inference.Confidence, strings.Join(inference.Evidence, ", "))
	}
	return []byte(b.String()), nil
}

func RenderArchitectureHTML(ir ArchitectureIR) ([]byte, error) {
	data, err := RenderArchitectureJSON(ir)
	if err != nil {
		return nil, err
	}
	var escaped bytes.Buffer
	for _, value := range string(data) {
		switch value {
		case '&':
			escaped.WriteString("&amp;")
		case '<':
			escaped.WriteString("&lt;")
		case '>':
			escaped.WriteString("&gt;")
		default:
			escaped.WriteRune(value)
		}
	}
	html := "<!doctype html><html lang=\"zh-CN\"><meta charset=\"utf-8\"><title>架构图</title><style>body{font-family:system-ui;margin:2rem;background:#10161c;color:#e8eef2}pre{white-space:pre-wrap;background:#17222c;padding:1rem;border-radius:.5rem}</style><h1>技术架构图</h1><pre>" + escaped.String() + "</pre></html>"
	return []byte(html), nil
}

func RenderArchitectureSVG(ir ArchitectureIR) ([]byte, error) {
	if err := ValidateArchitectureIR(ir); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="720" viewBox="0 0 1200 720"><rect width="1200" height="720" fill="#10161c"/><style>text{font:16px system-ui;fill:#e8eef2}.node{fill:#223442;stroke:#74c7b8}.edge{stroke:#9aaab5;stroke-width:2;marker-end:url(#arrow)}</style><defs><marker id="arrow" markerWidth="10" markerHeight="10" refX="8" refY="3" orient="auto"><path d="M0,0 L0,6 L9,3 z" fill="#9aaab5"/></marker></defs>`)
	if len(ir.Diagrams) == 0 {
		return nil, errors.New("architecture diagram is empty")
	}
	diagram := ir.Diagrams[0]
	positions := map[string][2]int{}
	for index, node := range diagram.Nodes {
		x := 80 + (index%4)*270
		y := 100 + (index/4)*120
		positions[node.ID] = [2]int{x, y}
		b.WriteString(fmt.Sprintf(`<rect class="node" x="%d" y="%d" width="220" height="56" rx="8"/><text x="%d" y="%d">%s</text>`, x, y, x+12, y+34, xmlEscape(node.Label)))
	}
	for _, edge := range diagram.Edges {
		from, ok1 := positions[edge.From]
		to, ok2 := positions[edge.To]
		if !ok1 || !ok2 {
			continue
		}
		b.WriteString(fmt.Sprintf(`<path class="edge" d="M%d %d L%d %d"/><text x="%d" y="%d">%s</text>`, from[0]+220, from[1]+28, to[0], to[1]+28, (from[0]+to[0])/2, (from[1]+to[1])/2-4, xmlEscape(edge.Label)))
	}
	b.WriteString("</svg>")
	return []byte(b.String()), nil
}

func xmlEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(value)
}

// RenderArchitecturePNG is a deterministic offline raster renderer. It uses
// the same validated IR as SVG and emits a real PNG signature without a
// browser, network request, or external process.
func RenderArchitecturePNG(ir ArchitectureIR) ([]byte, error) {
	if err := ValidateArchitectureIR(ir); err != nil {
		return nil, err
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 1200, 720))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.RGBA{R: 16, G: 22, B: 28, A: 255}}, image.Point{}, draw.Src)
	for index := range ir.Diagrams[0].Nodes {
		x := 80 + (index%4)*270
		y := 100 + (index/4)*120
		for yy := y; yy < y+56 && yy < canvas.Bounds().Max.Y; yy++ {
			for xx := x; xx < x+220 && xx < canvas.Bounds().Max.X; xx++ {
				canvas.Set(xx, yy, color.RGBA{R: 34, G: 52, B: 66, A: 255})
			}
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

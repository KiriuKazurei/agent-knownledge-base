package architecture

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

// ArchifySequenceInput and ArchifyDataflowInput are the small, typed subset
// of the vendored Archify schemas used by the architecture generator.  They
// intentionally contain no KAH-specific fields so the JSON passed to the
// upstream CLI can be validated independently of the application.
type ArchifySequenceInput struct {
	SchemaVersion int                  `json:"schema_version"`
	DiagramType   string               `json:"diagram_type"`
	Meta          archifyTypedMeta     `json:"meta"`
	Participants  []archifyParticipant `json:"participants"`
	Messages      []archifyMessage     `json:"messages"`
}

type archifyParticipant struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Label    string `json:"label"`
	Sublabel string `json:"sublabel,omitempty"`
}

// Archify keeps sequence participant boxes at 86px for the fixed layout and
// rejects labels whose estimated width exceeds that box. Keep a little room
// below the renderer's 92px validation threshold so changes to the font
// estimate cannot turn a valid display label into a failed architecture
// version.
const archifySequenceParticipantMaxUnits = 13

type archifyMessage struct {
	ID      string `json:"id,omitempty"`
	From    string `json:"from"`
	To      string `json:"to"`
	Y       int    `json:"y"`
	Label   string `json:"label"`
	Variant string `json:"variant,omitempty"`
	Note    string `json:"note,omitempty"`
}

type ArchifyDataflowInput struct {
	SchemaVersion int               `json:"schema_version"`
	DiagramType   string            `json:"diagram_type"`
	Meta          archifyTypedMeta  `json:"meta"`
	Stages        []archifyStage    `json:"stages"`
	Nodes         []archifyFlowNode `json:"nodes"`
	Flows         []archifyFlow     `json:"flows"`
}

type archifyTypedMeta struct {
	Title     string     `json:"title"`
	Locale    string     `json:"locale,omitempty"`
	Subtitle  string     `json:"subtitle,omitempty"`
	Quality   string     `json:"quality_profile,omitempty"`
	ColumnFit string     `json:"column_fit,omitempty"`
	ViewBox   [2]float64 `json:"viewBox,omitempty"`
}

type archifyStage struct {
	Label string `json:"label"`
}

type archifyFlowNode struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Label    string `json:"label"`
	Sublabel string `json:"sublabel,omitempty"`
	Stage    int    `json:"stage"`
	Row      int    `json:"row"`
}

type archifyFlow struct {
	ID             string      `json:"id,omitempty"`
	From           string      `json:"from"`
	To             string      `json:"to"`
	Label          string      `json:"label"`
	Classification string      `json:"classification,omitempty"`
	Variant        string      `json:"variant,omitempty"`
	Route          string      `json:"route,omitempty"`
	LabelAt        *[2]float64 `json:"labelAt,omitempty"`
}

func BuildArchifySequenceInput(project model.Project, snapshot model.ArchitectureSnapshot, ir ArchitectureIR) (ArchifySequenceInput, error) {
	if err := ValidateArchitectureIR(ir); err != nil {
		return ArchifySequenceInput{}, err
	}
	diagram := ir.Diagrams[2]
	participants := make([]archifyParticipant, 0, len(diagram.Nodes)+2)
	idMap := make(map[string]string, len(diagram.Nodes)+2)
	displayLabels := archifySequenceParticipantLabels(diagram.Nodes)
	for _, node := range diagram.Nodes {
		id := "participant-" + stableArchifyID(node.ID)
		if _, exists := idMap[node.ID]; exists {
			continue
		}
		idMap[node.ID] = id
		participants = append(participants, archifyParticipant{ID: id, Type: archifyComponentType(node.Kind), Label: displayLabels[node.ID], Sublabel: firstEvidence(node.Evidence)})
	}
	if len(participants) == 0 {
		participants = append(participants,
			archifyParticipant{ID: "participant-unknown-source", Type: "external", Label: "未知来源"},
			archifyParticipant{ID: "participant-unknown-target", Type: "external", Label: "未知目标"},
		)
		idMap["unknown-source"] = participants[0].ID
		idMap["unknown-target"] = participants[1].ID
	} else if len(participants) == 1 {
		participants = append(participants, archifyParticipant{ID: "participant-unknown-target", Type: "external", Label: "未知目标"})
		idMap["unknown-target"] = participants[1].ID
	}
	messages := make([]archifyMessage, 0, len(diagram.Edges)+1)
	for index, edge := range diagram.Edges {
		from, fromOK := idMap[edge.From]
		to, toOK := idMap[edge.To]
		if !fromOK || !toOK || from == to {
			continue
		}
		label := edge.Label
		if label == "" {
			label = edge.Relation
		}
		variant := "default"
		if edge.Unknown {
			variant, label = "dashed", "UNKNOWN: "+label
		}
		messages = append(messages, archifyMessage{ID: fmt.Sprintf("message-%d", index), From: from, To: to, Y: 160 + len(messages)*48, Label: archifyShortLabel(label, 44), Variant: variant})
	}
	if len(messages) == 0 {
		messages = append(messages, archifyMessage{ID: "message-unknown", From: participants[0].ID, To: participants[1].ID, Y: 160, Label: "UNKNOWN: 未声明调用顺序", Variant: "dashed", Note: "由证据不足产生的未知关系"})
	}
	width := math.Max(920, float64(len(participants)*108+160))
	height := math.Max(480, float64(240+len(messages)*48))
	title := strings.TrimSpace(project.Name)
	if title == "" {
		title = "项目"
	}
	return ArchifySequenceInput{
		SchemaVersion: 1,
		DiagramType:   "sequence",
		Meta:          archifyTypedMeta{Title: title + " 关键时序", Locale: "zh-CN", Subtitle: "快照 " + snapshot.ID, Quality: "standard", ColumnFit: "fixed", ViewBox: [2]float64{width, height}},
		Participants:  participants,
		Messages:      messages,
	}, nil
}

func BuildArchifyDataflowInput(project model.Project, snapshot model.ArchitectureSnapshot, ir ArchitectureIR) (ArchifyDataflowInput, error) {
	if err := ValidateArchitectureIR(ir); err != nil {
		return ArchifyDataflowInput{}, err
	}
	diagram := ir.Diagrams[3]
	stages := []archifyStage{{Label: "来源"}, {Label: "处理"}, {Label: "存储"}, {Label: "消费"}}
	flowNodes := make([]archifyFlowNode, 0, len(diagram.Nodes)+2)
	idMap := make(map[string]string, len(diagram.Nodes)+2)
	stageByID := make(map[string]int, len(diagram.Nodes)+2)
	rows := make([]int, len(stages))
	for index, node := range diagram.Nodes {
		id := "flow-" + stableArchifyID(node.ID)
		if _, exists := idMap[node.ID]; exists {
			continue
		}
		stage := index % len(stages)
		idMap[node.ID] = id
		stageByID[node.ID] = stage
		flowNodes = append(flowNodes, archifyFlowNode{ID: id, Type: archifyComponentType(node.Kind), Label: archifyShortLabel(node.Label, 18), Sublabel: firstEvidence(node.Evidence), Stage: stage, Row: rows[stage]})
		rows[stage]++
	}
	if len(flowNodes) == 0 {
		flowNodes = append(flowNodes,
			archifyFlowNode{ID: "flow-unknown-source", Type: "external", Label: "未知来源", Stage: 0, Row: 0},
			archifyFlowNode{ID: "flow-unknown-target", Type: "external", Label: "未知目标", Stage: 1, Row: 0},
		)
		idMap["unknown-source"], idMap["unknown-target"] = flowNodes[0].ID, flowNodes[1].ID
		stageByID["unknown-source"], stageByID["unknown-target"] = 0, 1
	} else if len(flowNodes) == 1 {
		flowNodes = append(flowNodes, archifyFlowNode{ID: "flow-unknown-target", Type: "external", Label: "未知目标", Stage: 1, Row: 0})
		idMap["unknown-target"], stageByID["unknown-target"] = flowNodes[1].ID, 1
	}
	flows := make([]archifyFlow, 0, len(diagram.Edges))
	for index, edge := range diagram.Edges {
		from, fromOK := idMap[edge.From]
		to, toOK := idMap[edge.To]
		if !fromOK || !toOK || from == to || stageByID[edge.From] == stageByID[edge.To] {
			continue
		}
		label := edge.Label
		if label == "" {
			label = edge.Relation
		}
		variant := "default"
		if edge.Unknown {
			variant, label = "dashed", "UNKNOWN: "+label
		}
		flows = append(flows, archifyFlow{ID: fmt.Sprintf("flow-%d", index), From: from, To: to, Label: archifyShortLabel(label, 38), Variant: variant, Route: "auto"})
	}
	if len(flows) == 0 {
		labelAt := [2]float64{208, 200}
		flows = append(flows, archifyFlow{ID: "flow-unknown", From: flowNodes[0].ID, To: flowNodes[1].ID, Label: "UNKNOWN: 未声明数据关系", Variant: "dashed", Route: "auto", LabelAt: &labelAt})
	}
	maxRows := 1
	for _, count := range rows {
		if count > maxRows {
			maxRows = count
		}
	}
	width := math.Max(940, float64(len(stages)*215+180))
	height := math.Max(720, float64(180+maxRows*114))
	title := strings.TrimSpace(project.Name)
	if title == "" {
		title = "项目"
	}
	return ArchifyDataflowInput{
		SchemaVersion: 1,
		DiagramType:   "dataflow",
		Meta:          archifyTypedMeta{Title: title + " 数据流", Locale: "zh-CN", Subtitle: "快照 " + snapshot.ID, Quality: "standard", ViewBox: [2]float64{width, height}},
		Stages:        stages,
		Nodes:         flowNodes,
		Flows:         flows,
	}, nil
}

func firstEvidence(values []string) string {
	if len(values) == 0 {
		return ""
	}
	value := strings.TrimSpace(values[0])
	if slash := strings.LastIndexAny(value, `/\\`); slash >= 0 && slash+1 < len(value) {
		value = value[slash+1:]
	}
	return truncateArchifyUnits(value, archifySequenceParticipantMaxUnits)
}

// archifySequenceParticipantLabels creates display-only labels for Archify.
// The original DiagramNode.Label and Evidence remain in the KAH IR and are
// used by the Markdown/JSON evidence views; only the bounded Archify label is
// normalized here. Entries are processed by stable node ID so collisions are
// resolved identically regardless of the order returned by an Agent.
func archifySequenceParticipantLabels(nodes []DiagramNode) map[string]string {
	type labelEntry struct {
		id   string
		base string
	}
	entries := make([]labelEntry, 0, len(nodes))
	for _, node := range nodes {
		base := compactArchifyParticipantLabel(node.Label)
		if base == "" {
			base = "参与者"
		}
		entries = append(entries, labelEntry{id: node.ID, base: base})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].id < entries[j].id })

	labels := make(map[string]string, len(entries))
	used := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		candidate := truncateArchifyUnits(entry.base, archifySequenceParticipantMaxUnits)
		if _, exists := used[candidate]; exists {
			for suffix := 2; ; suffix++ {
				candidate = archifyParticipantLabelWithSuffix(entry.base, "#"+strconv.Itoa(suffix))
				if _, exists := used[candidate]; !exists {
					break
				}
			}
		}
		labels[entry.id] = candidate
		used[candidate] = struct{}{}
	}
	return labels
}

func compactArchifyParticipantLabel(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	if slash := strings.LastIndexAny(value, `/\\`); slash >= 0 && slash+1 < len(value) {
		value = value[slash+1:]
	}
	// A package version is useful evidence but makes a fixed participant box
	// wider than the package name. The complete original value remains in the
	// source IR, so the sequence display can use the stable package name.
	if at := strings.LastIndexByte(value, '@'); at > 0 && looksLikePackageVersion(value[at+1:]) {
		value = value[:at]
	}

	words := strings.Fields(value)
	for index, word := range words {
		switch strings.ToLower(word) {
		case "configuration":
			words[index] = "cfg"
		case "application":
			words[index] = "app"
		case "service":
			words[index] = "svc"
		case "database":
			words[index] = "db"
		case "repository":
			words[index] = "repo"
		case "controller":
			words[index] = "ctrl"
		case "manager":
			words[index] = "mgr"
		case "implementation":
			words[index] = "impl"
		case "request":
			words[index] = "req"
		case "response":
			words[index] = "resp"
		case "processing":
			words[index] = "proc"
		}
	}
	return strings.Join(words, " ")
}

func looksLikePackageVersion(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(unicode.IsDigit(r) || r == '.') {
			return false
		}
	}
	return unicode.IsDigit(rune(value[0]))
}

func archifyParticipantLabelWithSuffix(base, suffix string) string {
	available := archifySequenceParticipantMaxUnits - archifyTextUnits(suffix)
	if available <= 0 {
		return truncateArchifyUnits(suffix, archifySequenceParticipantMaxUnits)
	}
	return truncateArchifyUnits(base, available) + suffix
}

func truncateArchifyUnits(value string, maxUnits int) string {
	value = strings.TrimSpace(value)
	if maxUnits <= 0 {
		return ""
	}
	if archifyTextUnits(value) <= maxUnits {
		return value
	}
	const ellipsis = "…"
	available := maxUnits - archifyTextUnits(ellipsis)
	if available <= 0 {
		return ellipsis
	}
	var b strings.Builder
	units := 0
	for _, r := range value {
		runeUnits := archifyRuneUnits(r)
		if units+runeUnits > available {
			break
		}
		b.WriteRune(r)
		units += runeUnits
	}
	return strings.TrimSpace(b.String()) + ellipsis
}

// This mirrors Archify's conservative text-width estimate closely enough for
// the display-only guard. Treating every non-ASCII rune as wide is deliberate:
// it leaves safety margin for full-width punctuation and CJK glyphs.
func archifyRuneUnits(r rune) int {
	if r == '…' {
		return 1
	}
	if r <= unicode.MaxASCII {
		return 1
	}
	return 2
}

func archifyTextUnits(value string) int {
	units := 0
	for _, r := range value {
		units += archifyRuneUnits(r)
	}
	return units
}

func archifyShortLabel(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

// Keep the conversion deterministic even when a scanner returns modules in a
// different order. This is used by callers that assemble custom IRs.
func sortArchifyNodes(nodes []DiagramNode) {
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
}

package model

import "time"

// GraphPosition is a node anchor in the fixed logical graph canvas. Values
// are percentages rather than CSS pixels so resizing and viewport transforms
// never alter the saved relative layout.
type GraphPosition struct {
	XPct float64 `json:"xPct"`
	YPct float64 `json:"yPct"`
}

type GraphWorkspace struct {
	ID        string         `json:"id"`
	LibraryID string         `json:"libraryId"`
	Name      string         `json:"name"`
	Filters   map[string]any `json:"filters"`
	Viewport  map[string]any `json:"viewport"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

type GraphNode struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	Label       string         `json:"label"`
	Description string         `json:"description,omitempty"`
	URI         string         `json:"uri"`
	Revision    int            `json:"revision,omitempty"`
	Status      string         `json:"status,omitempty"`
	Trust       string         `json:"trust,omitempty"`
	Tags        []string       `json:"tags"`
	Position    *GraphPosition `json:"position"`
}

type GraphEdge struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	RelationType   string         `json:"relationType"`
	Source         string         `json:"source"`
	Target         string         `json:"target"`
	TargetRevision int            `json:"targetRevision,omitempty"`
	SourceID       string         `json:"sourceId,omitempty"`
	Locator        map[string]any `json:"locator,omitempty"`
	Snapshot       map[string]any `json:"snapshot,omitempty"`
}

type GraphLimits struct {
	RequestedDepth int `json:"requestedDepth"`
	AppliedDepth   int `json:"appliedDepth"`
	MaxNodes       int `json:"maxNodes"`
}

type GraphSnapshot struct {
	Workspace   GraphWorkspace `json:"workspace"`
	Nodes       []GraphNode    `json:"nodes"`
	Edges       []GraphEdge    `json:"edges"`
	Truncated   bool           `json:"truncated"`
	HiddenNodes int            `json:"hiddenNodeCount"`
	Limits      GraphLimits    `json:"limits"`
}

type GraphPositionUpdate struct {
	NodeID string  `json:"nodeId"`
	XPct   float64 `json:"xPct"`
	YPct   float64 `json:"yPct"`
}

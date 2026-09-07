package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
)

func TestGraphHTTPWorkspacePositionsAndStableScope(t *testing.T) {
	server, handler := testServer(t)
	library, err := server.Store.CreateLibrary(t.Context(), "Graph API", "")
	if err != nil {
		t.Fatal(err)
	}
	submission, _, err := server.Store.CreateKnowledgeDraft(t.Context(), storage.KnowledgeDraftInput{LibraryID: library.ID, ClientSubmissionID: "graph-api", Mode: "create", Payload: model.KnowledgePayload{
		Schema: storage.KAHKnowledgeSchema, Type: "claim", Title: "Graph API node", Description: "Stable graph node", Language: "en",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "Claim", Content: "The graph API exposes stable knowledge."}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Store.ReviewKAHSubmission(t.Context(), submission.ID, "graph-test", "approve", ""); err != nil {
		t.Fatal(err)
	}
	published, err := server.Store.PublishKAHSubmission(t.Context(), submission.ID)
	if err != nil {
		t.Fatal(err)
	}

	read := request(t, handler, http.MethodGet, "/api/v1/graph?libraryId="+library.ID+"&depth=9&limit=999", nil, "desktop-test")
	if read.Code != http.StatusOK {
		t.Fatalf("read graph: %d %s", read.Code, read.Body.String())
	}
	var snapshot model.GraphSnapshot
	if err := json.Unmarshal(read.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].URI != published.URI || snapshot.Limits.AppliedDepth != 2 || snapshot.Limits.MaxNodes != 300 {
		t.Fatalf("unexpected graph response: %+v", snapshot)
	}
	nodeID := snapshot.Nodes[0].ID
	positions := request(t, handler, http.MethodPut, "/api/v1/graph/workspaces/"+snapshot.Workspace.ID+"/positions", map[string]any{"positions": []map[string]any{{"nodeId": nodeID, "xPct": 37.6254, "yPct": 14.0834}}}, "desktop-test")
	if positions.Code != http.StatusOK || !json.Valid(positions.Body.Bytes()) {
		t.Fatalf("save graph positions: %d %s", positions.Code, positions.Body.String())
	}
	read = request(t, handler, http.MethodGet, "/api/v1/graph?libraryId="+library.ID+"&workspaceId="+snapshot.Workspace.ID, nil, "desktop-test")
	if err := json.Unmarshal(read.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Nodes[0].Position == nil || snapshot.Nodes[0].Position.XPct != 37.625 || snapshot.Nodes[0].Position.YPct != 14.083 {
		t.Fatalf("fractional graph position was not returned: %+v", snapshot.Nodes[0])
	}
	updated := request(t, handler, http.MethodPatch, "/api/v1/graph/workspaces/"+snapshot.Workspace.ID, map[string]any{"name": "研究图谱", "filters": map[string]any{"showSources": true}, "viewport": map[string]any{"scale": 1.25, "panX": 4.5}}, "desktop-test")
	if updated.Code != http.StatusOK || !json.Valid(updated.Body.Bytes()) {
		t.Fatalf("update graph workspace: %d %s", updated.Code, updated.Body.String())
	}
	layout := request(t, handler, http.MethodPost, "/api/v1/graph/workspaces/"+snapshot.Workspace.ID+"/auto-layout", map[string]any{"libraryId": library.ID, "overwrite": true}, "desktop-test")
	if layout.Code != http.StatusOK || !json.Valid(layout.Body.Bytes()) {
		t.Fatalf("auto layout graph: %d %s", layout.Code, layout.Body.String())
	}
}

func TestGraphHTTPRejectsInvalidAndUnauthorizedScope(t *testing.T) {
	_, handler := testServer(t)
	if response := request(t, handler, http.MethodGet, "/api/v1/graph", nil, "desktop-test"); response.Code != http.StatusBadRequest {
		t.Fatalf("missing libraryId status = %d", response.Code)
	}
	if response := request(t, handler, http.MethodGet, "/api/v1/graph?libraryId=missing&depth=nope", nil, "desktop-test"); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid depth status = %d", response.Code)
	}
	if response := request(t, handler, http.MethodGet, "/api/v1/graph?libraryId=missing", nil, "bad-token"); response.Code != http.StatusUnauthorized {
		t.Fatalf("non-desktop graph access status = %d", response.Code)
	}
}

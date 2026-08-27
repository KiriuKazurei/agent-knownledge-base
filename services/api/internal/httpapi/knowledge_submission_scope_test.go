package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestKnowledgeSubmissionReadRequiresSubmitScope(t *testing.T) {
	_, handler := testServer(t)
	libraryID := createStage4Library(t, handler, "提交作用域库")
	response := request(t, handler, http.MethodPost, "/api/v1/tokens", map[string]any{
		"name": "query-only", "scopes": []string{"query"}, "libraryIds": []string{libraryID},
	}, "desktop-test")
	if response.Code != http.StatusCreated {
		t.Fatalf("create query token: %d %s", response.Code, response.Body.String())
	}
	var token model.AgentToken
	if err := json.Unmarshal(response.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	list := request(t, handler, http.MethodGet, "/api/v1/knowledge-submissions", nil, token.Secret)
	if list.Code != http.StatusForbidden {
		t.Fatalf("query-only token read submissions: %d %s", list.Code, list.Body.String())
	}
}

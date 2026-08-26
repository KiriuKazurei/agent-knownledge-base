package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func createStage4Library(t *testing.T, handler http.Handler, name string) string {
	t.Helper()
	response := request(t, handler, http.MethodPost, "/api/v1/libraries", map[string]any{"name": name}, "desktop-test")
	if response.Code != http.StatusCreated {
		t.Fatalf("create library: %d %s", response.Code, response.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	id, _ := item["id"].(string)
	if id == "" {
		t.Fatalf("library id missing: %s", response.Body.String())
	}
	return id
}

func createStage4Chunk(t *testing.T, server *Server, libraryID, chunkID, text string) {
	t.Helper()
	doc, err := server.Store.CreatePendingDocument(context.Background(), libraryID, "stage4.md", "text/markdown", "stage4.md", "", "objects/stage4", "stage4-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Store.ReplaceChunks(context.Background(), doc.ID, []model.Chunk{{ID: chunkID, DocumentID: doc.ID, Text: text, Location: map[string]any{"ordinal": 0}, ContentHash: "stage4-hash"}}); err != nil {
		t.Fatal(err)
	}
}

func TestStage4AnswerPrivacyStreamAndAudit(t *testing.T) {
	server, handler := testServer(t)
	libraryID := createStage4Library(t, handler, "Stage4")
	createStage4Chunk(t, server, libraryID, "stage4-chunk", "portable evidence for the answer")

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"grounded answer [1]\"}}]}"))
	}))
	defer providerServer.Close()

	providerResponse := request(t, handler, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "Custom test endpoint", "kind": "custom", "baseUrl": providerServer.URL, "model": "test-model", "local": true,
	}, "desktop-test")
	if providerResponse.Code != http.StatusOK {
		t.Fatalf("save provider: %d %s", providerResponse.Code, providerResponse.Body.String())
	}
	var provider map[string]any
	if err := json.Unmarshal(providerResponse.Body.Bytes(), &provider); err != nil {
		t.Fatal(err)
	}
	providerID, _ := provider["id"].(string)
	if provider["local"] == true {
		t.Fatal("custom remote provider was trusted as local")
	}

	answerRequest := map[string]any{"query": "portable evidence", "libraryIds": []string{libraryID}, "responseMode": "answer", "providerId": providerID}
	denied := request(t, handler, http.MethodPost, "/api/v1/query", answerRequest, "desktop-test")
	if denied.Code != http.StatusBadRequest || !strings.Contains(denied.Body.String(), "not allowed") {
		t.Fatalf("expected privacy denial: %d %s", denied.Code, denied.Body.String())
	}
	allowed := request(t, handler, http.MethodPatch, "/api/v1/libraries/"+libraryID, map[string]any{"allowRemoteModels": true}, "desktop-test")
	if allowed.Code != http.StatusOK {
		t.Fatalf("allow remote: %d %s", allowed.Code, allowed.Body.String())
	}
	answer := request(t, handler, http.MethodPost, "/api/v1/query", answerRequest, "desktop-test")
	if answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), "grounded answer [1]") {
		t.Fatalf("answer: %d %s", answer.Code, answer.Body.String())
	}
	stream := request(t, handler, http.MethodPost, "/api/v1/query/stream", map[string]any{"query": "portable evidence", "libraryIds": []string{libraryID}}, "desktop-test")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: retrieval") || !strings.Contains(stream.Body.String(), "event: citation") || !strings.Contains(stream.Body.String(), "event: complete") {
		t.Fatalf("stream: %d %s", stream.Code, stream.Body.String())
	}
	streamError := request(t, handler, http.MethodPost, "/api/v1/query/stream", map[string]any{"query": "portable evidence", "libraryIds": []string{libraryID}, "responseMode": "answer"}, "desktop-test")
	if streamError.Code != http.StatusOK || !strings.Contains(streamError.Body.String(), "event: error") {
		t.Fatalf("stream error event: %d %s", streamError.Code, streamError.Body.String())
	}
	audit := request(t, handler, http.MethodGet, "/api/v1/audit", nil, "desktop-test")
	if audit.Code != http.StatusOK || !strings.Contains(audit.Body.String(), "provider_saved") || !strings.Contains(audit.Body.String(), "query_completed") {
		t.Fatalf("audit: %d %s", audit.Code, audit.Body.String())
	}
}

func TestStage4TokenFeedbackScopeAndRevocation(t *testing.T) {
	server, handler := testServer(t)
	allowedLibrary := createStage4Library(t, handler, "Allowed")
	foreignLibrary := createStage4Library(t, handler, "Foreign")
	createStage4Chunk(t, server, allowedLibrary, "allowed-chunk", "allowed feedback evidence")
	createStage4Chunk(t, server, foreignLibrary, "foreign-chunk", "foreign feedback evidence")

	tokenResponse := request(t, handler, http.MethodPost, "/api/v1/tokens", map[string]any{"name": "stage4-agent", "scopes": []string{"feedback"}, "libraryIds": []string{allowedLibrary}}, "desktop-test")
	if tokenResponse.Code != http.StatusCreated {
		t.Fatalf("create token: %d %s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var token map[string]any
	if err := json.Unmarshal(tokenResponse.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	tokenID, _ := token["id"].(string)
	tokenSecret, _ := token["secret"].(string)
	feedback := request(t, handler, http.MethodPost, "/api/v1/feedback", map[string]any{"requestId": "query-1", "chunkId": "allowed-chunk", "relevant": true}, tokenSecret)
	if feedback.Code != http.StatusNoContent {
		t.Fatalf("allowed feedback: %d %s", feedback.Code, feedback.Body.String())
	}
	foreign := request(t, handler, http.MethodPost, "/api/v1/feedback", map[string]any{"requestId": "query-1", "chunkId": "foreign-chunk", "relevant": false}, tokenSecret)
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("foreign feedback: %d %s", foreign.Code, foreign.Body.String())
	}
	revoke := request(t, handler, http.MethodDelete, "/api/v1/tokens/"+tokenID, nil, "desktop-test")
	if revoke.Code != http.StatusNoContent {
		t.Fatalf("revoke token: %d %s", revoke.Code, revoke.Body.String())
	}
	revoked := request(t, handler, http.MethodPost, "/api/v1/feedback", map[string]any{"requestId": "query-1", "chunkId": "allowed-chunk", "relevant": true}, tokenSecret)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked feedback: %d %s", revoked.Code, revoked.Body.String())
	}
}

func TestStage4SSERejectsInvalidRequest(t *testing.T) {
	_, handler := testServer(t)
	response := request(t, handler, http.MethodPost, "/api/v1/query/stream", map[string]any{}, "desktop-test")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid request: %d %s", response.Code, response.Body.String())
	}
}

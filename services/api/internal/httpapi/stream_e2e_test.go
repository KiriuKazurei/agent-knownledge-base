package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestQueryStreamForwardsProviderDeltas(t *testing.T) {
	t.Skip("superseded: cited answer endpoint was removed in the KAH MCP migration")
	server, handler := testServer(t)
	libraryID := createStage4Library(t, handler, "真实流式")
	createStage4Chunk(t, server, libraryID, "stream-chunk", "流式回答的事实证据")

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"第一段\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"第二段 [1]\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer providerServer.Close()

	providerResponse := request(t, handler, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "流式测试端点", "kind": "lmstudio", "baseUrl": providerServer.URL, "model": "stream-model",
	}, "desktop-test")
	if providerResponse.Code != http.StatusOK {
		t.Fatalf("save provider: %d %s", providerResponse.Code, providerResponse.Body.String())
	}
	var provider model.Provider
	if err := json.Unmarshal(providerResponse.Body.Bytes(), &provider); err != nil {
		t.Fatal(err)
	}
	stream := request(t, handler, http.MethodPost, "/api/v1/query/stream", map[string]any{
		"query": "流式回答", "libraryIds": []string{libraryID}, "responseMode": "answer", "providerId": provider.ID,
	}, "desktop-test")
	body := stream.Body.String()
	if stream.Code != http.StatusOK || !strings.Contains(body, "第一段") || !strings.Contains(body, "第二段 [1]") || !strings.Contains(body, "event: complete") {
		t.Fatalf("stream response: %d %s", stream.Code, body)
	}
	if strings.Count(body, "event: answer_delta") != 2 {
		t.Fatalf("expected two provider deltas: %s", body)
	}
}

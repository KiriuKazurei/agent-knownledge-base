package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestGenerateStreamReportsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"rate limited\"}}\n\n"))
	}))
	defer server.Close()

	err := New().GenerateStream(context.Background(), model.Provider{Kind: "custom", BaseURL: server.URL, Model: "test"}, "", "问题", nil, func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected provider stream error, got %v", err)
	}
}

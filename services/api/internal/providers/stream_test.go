package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestGenerateStreamOpenAICompatibleSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"中文\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"流式\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	var deltas []string
	err := New().GenerateStream(context.Background(), model.Provider{Kind: "custom", BaseURL: server.URL, Model: "test"}, "secret", "问题", []model.Evidence{{Chunk: model.Chunk{Text: "事实"}, Title: "证据"}}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deltas, []string{"中文", "流式"}) {
		t.Fatalf("unexpected deltas: %#v", deltas)
	}
}

func TestGenerateStreamAnthropicSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "anthropic-secret" {
			t.Fatalf("provider key was not sent")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"安\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"全\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	var answer string
	err := New().GenerateStream(context.Background(), model.Provider{Kind: "anthropic", BaseURL: server.URL, Model: "test"}, "anthropic-secret", "问题", nil, func(delta string) error {
		answer += delta
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "安全" {
		t.Fatalf("unexpected anthropic answer: %q", answer)
	}
}

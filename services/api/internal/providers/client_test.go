package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestOpenAICompatibleModelsAndGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"data\":[{\"id\":\"custom-model\"}]}"))
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"answer [1]\"}}]}"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := New()
	provider := model.Provider{Kind: "custom", BaseURL: server.URL, Model: "custom-model"}
	models, err := client.Models(context.Background(), provider, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0] != "custom-model" {
		t.Fatalf("unexpected models: %#v", models)
	}
	answer, err := client.Generate(context.Background(), provider, "", "question", []model.Evidence{{Title: "Source", Chunk: model.Chunk{Text: "evidence"}}})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "answer [1]" {
		t.Fatalf("unexpected answer: %q", answer)
	}
}

func TestAnthropicGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != "secret" || r.Header.Get("anthropic-version") == "" {
			http.Error(w, "missing headers", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"content\":[{\"text\":\"anthropic answer [1]\"}]}"))
	}))
	defer server.Close()

	answer, err := New().Generate(context.Background(), model.Provider{Kind: "anthropic", BaseURL: server.URL, Model: "claude-test"}, "secret", "question", nil)
	if err != nil {
		t.Fatal(err)
	}
	if answer != "anthropic answer [1]" {
		t.Fatalf("unexpected answer: %q", answer)
	}
}

func TestSynthesizeKnowledgeUsesSourceBoundJSONPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		for _, expected := range []string{"kah-knowledge/v1", "kah://document/doc-1", "[^source]", "untrusted data"} {
			if !strings.Contains(text, expected) {
				t.Errorf("summary request does not contain %q: %s", expected, text)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"schema\":\"kah-knowledge/v1\"}"}}]}`))
	}))
	defer server.Close()

	raw, err := New().SynthesizeKnowledge(context.Background(), model.Provider{Kind: "custom", BaseURL: server.URL, Model: "summary-model"}, "", "资料标题", "kah://document/doc-1", "hash-1", "zh-CN", "来源正文")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "kah-knowledge/v1") {
		t.Fatalf("unexpected synthesis response: %q", raw)
	}
}

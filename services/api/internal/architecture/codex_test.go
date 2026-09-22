package architecture

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("KAH_CODEX_HELPER") == "1" {
		codexHelper()
		return
	}
	os.Exit(m.Run())
}

func codexHelper() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		_, _ = os.Stdout.WriteString("codex fake 1.0\n")
		os.Exit(0)
	}
	if len(args) >= 2 && args[0] == "exec" && args[1] == "--help" {
		_, _ = os.Stdout.WriteString("--json --output-schema -o -s read-only -C\n")
		os.Exit(0)
	}
	if os.Getenv("KAH_CODEX_DENY") == "1" {
		_, _ = os.Stderr.WriteString("permission denied by sandbox\n")
		os.Exit(3)
	}
	if os.Getenv("KAH_CODEX_SLEEP") == "1" {
		time.Sleep(5 * time.Second)
	}
	for i, arg := range args {
		if arg == "-o" && i+1 < len(args) {
			payload := map[string]any{"schemaVersion": "kah.architecture.analysis.v1", "facts": []any{}, "inferences": []any{}, "evidence": []any{}, "incremental": map[string]any{"added": []string{}, "changed": []string{}, "deleted": []string{}, "unchanged": []string{}}, "coverage": map[string]any{}}
			data, _ := json.Marshal(payload)
			_ = os.WriteFile(args[i+1], data, 0o600)
		}
	}
	_, _ = os.Stdout.WriteString(`{"type":"turn.completed"}` + "\n")
	os.Exit(0)
}

func fakeCodexConfig() CodexConfig {
	return CodexConfig{Path: os.Args[0], Timeout: 5 * time.Second}
}

func TestExecuteCodexUsesStructuredReadOnlyArgumentsAndParsesEvents(t *testing.T) {
	t.Setenv("KAH_CODEX_HELPER", "1")
	root := t.TempDir()
	result, err := ExecuteCodex(context.Background(), fakeCodexConfig(), root, "prompt --looks-like-a-flag", []byte(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 1 || result.ContentHash == "" || !json.Valid(result.Raw) {
		t.Fatalf("structured result incomplete: %+v", result)
	}
	if !strings.Contains(result.Capability.Version, "codex fake") {
		t.Fatalf("capability version missing: %+v", result.Capability)
	}
}

func TestExecuteCodexClassifiesPermissionAndCancellationWithoutFallback(t *testing.T) {
	t.Setenv("KAH_CODEX_HELPER", "1")
	t.Setenv("KAH_CODEX_DENY", "1")
	_, err := ExecuteCodex(context.Background(), fakeCodexConfig(), t.TempDir(), "prompt", []byte(`{"type":"object"}`))
	if !errors.Is(err, ErrExecutionDenied) {
		t.Fatalf("expected permission denial, got %v", err)
	}
	t.Setenv("KAH_CODEX_DENY", "")
	t.Setenv("KAH_CODEX_SLEEP", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = ExecuteCodex(ctx, fakeCodexConfig(), t.TempDir(), "prompt", []byte(`{"type":"object"}`))
	if !errors.Is(err, ErrExecutionCanceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestDetectCodexRejectsMissingRequiredCapability(t *testing.T) {
	_ = filepath.Separator
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := DetectCodex(ctx, filepath.Join(t.TempDir(), "missing-codex")); !errors.Is(err, ErrCodexUnavailable) {
		t.Fatalf("expected unavailable codex, got %v", err)
	}
}

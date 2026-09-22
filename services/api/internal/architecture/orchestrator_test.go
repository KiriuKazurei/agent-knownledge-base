package architecture

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func fakeAnalysisExecutor(name string, available bool, calls *[]string, err error) AnalysisExecutor {
	return FunctionAnalyzer{ExecutorName: name, Check: func(context.Context, model.Project) bool { return available }, Run: func(context.Context, model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error) {
		*calls = append(*calls, name)
		if err != nil {
			return model.ArchitectureAnalysis{}, err
		}
		return model.ArchitectureAnalysis{SchemaVersion: "kah.architecture.analysis.v1"}, nil
	}}
}

func TestAnalysisChainStrictOrderAndRemotePolicy(t *testing.T) {
	calls := []string{}
	chain := AnalysisChain{Codex: fakeAnalysisExecutor("codex", true, &calls, errors.New("codex unavailable")), MCP: fakeAnalysisExecutor("mcp", true, &calls, nil), Provider: fakeAnalysisExecutor("provider", true, &calls, nil), MaxAttempts: 1}
	request := model.ArchitectureAnalysisRequest{Project: model.Project{ID: "p", AllowRemoteCode: true, Execution: model.ProjectExecutionConfig{Executor: "auto"}}, Snapshot: model.ArchitectureSnapshot{ID: "s"}}
	result, err := chain.Run(context.Background(), request)
	if err != nil || result.Executor != "mcp" {
		t.Fatalf("expected MCP fallback, result=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(calls, []string{"codex", "mcp"}) {
		t.Fatalf("executor order: %v", calls)
	}

	calls = nil
	request.Project.AllowRemoteCode = false
	_, err = chain.Run(context.Background(), request)
	if err == nil || !reflect.DeepEqual(calls, []string{"codex"}) {
		t.Fatalf("remote policy was bypassed, calls=%v err=%v", calls, err)
	}
}

func TestAnalysisChainPermissionAndCancellationAreTerminal(t *testing.T) {
	for _, terminal := range []error{ErrExecutionDenied, ErrExecutionCanceled} {
		calls := []string{}
		chain := AnalysisChain{Codex: fakeAnalysisExecutor("codex", true, &calls, terminal), MCP: fakeAnalysisExecutor("mcp", true, &calls, nil), MaxAttempts: 3}
		request := model.ArchitectureAnalysisRequest{Project: model.Project{AllowRemoteCode: true, Execution: model.ProjectExecutionConfig{Executor: "auto"}}}
		_, err := chain.Run(context.Background(), request)
		if !errors.Is(err, terminal) || !reflect.DeepEqual(calls, []string{"codex"}) {
			t.Fatalf("terminal error fell through: terminal=%v calls=%v err=%v", terminal, calls, err)
		}
	}
}

func TestAnalysisChainRetriesTransientFailureBeforeFallback(t *testing.T) {
	calls := []string{}
	attempt := 0
	codex := FunctionAnalyzer{ExecutorName: "codex", Check: func(context.Context, model.Project) bool { return true }, Run: func(context.Context, model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error) {
		calls = append(calls, "codex")
		attempt++
		if attempt < 3 {
			return model.ArchitectureAnalysis{}, errors.New("temporary")
		}
		return model.ArchitectureAnalysis{SchemaVersion: "kah.architecture.analysis.v1"}, nil
	}}
	chain := AnalysisChain{Codex: codex, MaxAttempts: 3, RetryDelay: 1}
	result, err := chain.Run(context.Background(), model.ArchitectureAnalysisRequest{Project: model.Project{Execution: model.ProjectExecutionConfig{Executor: "codex"}}})
	if err != nil || result.Attempts != 3 || len(calls) != 3 {
		t.Fatalf("retry contract failed: result=%+v calls=%v err=%v", result, calls, err)
	}
}

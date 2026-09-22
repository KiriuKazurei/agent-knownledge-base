package architecture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

var ErrNoArchitectureExecutor = errors.New("no architecture analysis executor is available")

type AnalysisExecutor interface {
	Name() string
	Available(context.Context, model.Project) bool
	Analyze(context.Context, model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error)
}

type remoteAwareExecutor interface {
	Remote(context.Context, model.Project) bool
}

type AnalysisChain struct {
	Codex       AnalysisExecutor
	MCP         AnalysisExecutor
	Provider    AnalysisExecutor
	MaxAttempts int
	RetryDelay  time.Duration
}

type AnalysisRunResult struct {
	Analysis model.ArchitectureAnalysis
	Executor string
	Attempts int
}

// Run chooses exactly one path in the documented order. A permission denial
// or cancellation is terminal and is never turned into a fallback request.
func (c AnalysisChain) Run(ctx context.Context, request model.ArchitectureAnalysisRequest) (AnalysisRunResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ordered := []AnalysisExecutor{}
	choice := strings.ToLower(strings.TrimSpace(request.Project.Execution.Executor))
	if choice == "" || choice == "auto" {
		ordered = []AnalysisExecutor{c.Codex, c.MCP, c.Provider}
	} else {
		switch choice {
		case "codex":
			ordered = []AnalysisExecutor{c.Codex}
		case "mcp":
			ordered = []AnalysisExecutor{c.MCP}
		case "provider":
			ordered = []AnalysisExecutor{c.Provider}
		default:
			return AnalysisRunResult{}, fmt.Errorf("unsupported architecture executor %q", choice)
		}
	}
	maxAttempts := c.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 2
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = 100 * time.Millisecond
	}
	var lastErr error
	for _, executor := range ordered {
		if executor == nil || !executor.Available(ctx, request.Project) {
			continue
		}
		remote := executor.Name() == "mcp" || executor.Name() == "provider"
		if aware, ok := executor.(remoteAwareExecutor); ok {
			remote = aware.Remote(ctx, request.Project)
		}
		if remote && !request.Project.AllowRemoteCode {
			continue
		}
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			analysis, err := executor.Analyze(ctx, request)
			if err == nil {
				if analysis.ProjectID == "" {
					analysis.ProjectID = request.Project.ID
				}
				if analysis.SnapshotID == "" {
					analysis.SnapshotID = request.Snapshot.ID
				}
				return AnalysisRunResult{Analysis: analysis, Executor: executor.Name(), Attempts: attempt}, nil
			}
			lastErr = err
			if errors.Is(err, ErrExecutionDenied) || errors.Is(err, ErrExecutionCanceled) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return AnalysisRunResult{Executor: executor.Name(), Attempts: attempt}, err
			}
			if attempt < maxAttempts {
				select {
				case <-ctx.Done():
					return AnalysisRunResult{Executor: executor.Name(), Attempts: attempt}, ErrExecutionCanceled
				case <-time.After(c.RetryDelay):
				}
			}
		}
	}
	if lastErr != nil {
		return AnalysisRunResult{}, lastErr
	}
	return AnalysisRunResult{}, ErrNoArchitectureExecutor
}

type CodexAnalyzer struct {
	Config CodexConfig
	Schema []byte
}

func (a CodexAnalyzer) Name() string { return "codex" }
func (a CodexAnalyzer) Available(ctx context.Context, project model.Project) bool {
	_, err := DetectCodex(ctx, firstNonEmpty(a.Config.Path, project.Execution.CodexPath))
	return err == nil
}
func (a CodexAnalyzer) Analyze(ctx context.Context, request model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error) {
	cfg := a.Config
	if cfg.Path == "" {
		cfg.Path = request.Project.Execution.CodexPath
	}
	if cfg.Model == "" {
		cfg.Model = request.Project.Execution.CodexModel
	}
	if cfg.Timeout <= 0 && request.Project.Execution.TimeoutSeconds > 0 {
		cfg.Timeout = time.Duration(request.Project.Execution.TimeoutSeconds) * time.Second
	}
	schema := a.Schema
	if len(schema) == 0 {
		schema = []byte(`{"type":"object","required":["schemaVersion","facts","inferences","evidence","incremental","coverage"]}`)
	}
	prompt := request.Prompt
	if prompt == "" {
		prompt = "Analyze the provided read-only architecture snapshot. Return only the requested structured JSON. Every fact and inference must cite snapshot evidence."
	}
	payload, err := ExecuteCodex(ctx, cfg, request.Project.RootPath, prompt, schema)
	if err != nil {
		return model.ArchitectureAnalysis{}, err
	}
	var analysis model.ArchitectureAnalysis
	if err := json.Unmarshal(payload.Raw, &analysis); err != nil {
		return model.ArchitectureAnalysis{}, fmt.Errorf("decode codex architecture result: %w", err)
	}
	if analysis.SchemaVersion == "" {
		analysis.SchemaVersion = "kah.architecture.analysis.v1"
	}
	if analysis.ProjectID == "" {
		analysis.ProjectID = request.Project.ID
	}
	if analysis.SnapshotID == "" {
		analysis.SnapshotID = request.Snapshot.ID
	}
	if analysis.Incremental.BaseSnapshotID == "" {
		analysis.Incremental = request.Incremental
	}
	return analysis, nil
}

type FunctionAnalyzer struct {
	ExecutorName string
	Check        func(context.Context, model.Project) bool
	RemoteCheck  func(context.Context, model.Project) bool
	Run          func(context.Context, model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error)
}

func (f FunctionAnalyzer) Name() string { return f.ExecutorName }
func (f FunctionAnalyzer) Available(ctx context.Context, project model.Project) bool {
	return f.Run != nil && (f.Check == nil || f.Check(ctx, project))
}
func (f FunctionAnalyzer) Analyze(ctx context.Context, request model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error) {
	if f.Run == nil {
		return model.ArchitectureAnalysis{}, ErrNoArchitectureExecutor
	}
	return f.Run(ctx, request)
}

func (f FunctionAnalyzer) Remote(ctx context.Context, project model.Project) bool {
	if f.RemoteCheck != nil {
		return f.RemoteCheck(ctx, project)
	}
	return f.ExecutorName == "mcp" || f.ExecutorName == "provider"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

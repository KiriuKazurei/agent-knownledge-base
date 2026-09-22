package architecture

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrCodexUnavailable  = errors.New("codex CLI is unavailable")
	ErrExecutionDenied   = errors.New("architecture execution was denied by the executor")
	ErrExecutionCanceled = errors.New("architecture execution was cancelled")
)

type CodexCapability struct {
	Path             string `json:"path"`
	Version          string `json:"version,omitempty"`
	Available        bool   `json:"available"`
	SupportsJSON     bool   `json:"supportsJson"`
	SupportsSchema   bool   `json:"supportsSchema"`
	SupportsReadOnly bool   `json:"supportsReadOnly"`
}

type CodexConfig struct {
	Path    string
	Model   string
	Timeout time.Duration
}

type CodexEvent struct {
	Type   string          `json:"type,omitempty"`
	Raw    json.RawMessage `json:"raw,omitempty"`
	Error  string          `json:"error,omitempty"`
	Status string          `json:"status,omitempty"`
}

type CodexResult struct {
	Events      []CodexEvent    `json:"events"`
	Raw         json.RawMessage `json:"raw"`
	ContentHash string          `json:"contentHash"`
	Capability  CodexCapability `json:"capability"`
}

// DetectCodex probes only local executable metadata/help. It never starts an
// interactive session or performs a network operation.
func DetectCodex(ctx context.Context, configuredPath string) (CodexCapability, error) {
	path := strings.TrimSpace(configuredPath)
	if path == "" {
		resolved, err := exec.LookPath("codex")
		if err != nil {
			return CodexCapability{}, ErrCodexUnavailable
		}
		path = resolved
	}
	capability := CodexCapability{Path: path}
	versionOutput, err := runLocalCommand(ctx, path, "--version")
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return capability, ErrExecutionCanceled
		}
		return capability, fmt.Errorf("%w: %v", ErrCodexUnavailable, err)
	}
	capability.Available = true
	capability.Version = strings.TrimSpace(versionOutput)
	helpOutput, err := runLocalCommand(ctx, path, "exec", "--help")
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return capability, ErrExecutionCanceled
		}
		return capability, fmt.Errorf("codex exec help: %w", err)
	}
	capability.SupportsJSON = containsFlag(helpOutput, "--json")
	capability.SupportsSchema = containsFlag(helpOutput, "--output-schema")
	capability.SupportsReadOnly = containsFlag(helpOutput, "read-only") || containsFlag(helpOutput, "-s")
	if !capability.SupportsJSON || !capability.SupportsSchema || !capability.SupportsReadOnly {
		return capability, errors.New("codex CLI does not support the required non-interactive structured read-only flags")
	}
	return capability, nil
}

func containsFlag(output, flag string) bool {
	return strings.Contains(strings.ToLower(output), strings.ToLower(flag))
}

func runLocalCommand(ctx context.Context, path string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, path, args...)
	data, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return string(data), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(data)))
	}
	return string(data), nil
}

// ExecuteCodex always supplies its own fixed argument vector. The prompt is
// passed as one final argument, and user/project text can never inject flags.
func ExecuteCodex(ctx context.Context, cfg CodexConfig, projectRoot, prompt string, outputSchema []byte) (CodexResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return CodexResult{}, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("project root is not a directory")
		}
		return CodexResult{}, err
	}
	if len(outputSchema) == 0 {
		return CodexResult{}, errors.New("output schema is required")
	}
	capability, err := DetectCodex(ctx, cfg.Path)
	if err != nil {
		return CodexResult{Capability: capability}, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	temp, err := os.MkdirTemp("", "kah-codex-")
	if err != nil {
		return CodexResult{}, err
	}
	defer os.RemoveAll(temp)
	schemaPath := filepath.Join(temp, "analysis.schema.json")
	outputPath := filepath.Join(temp, "analysis.result.json")
	if err := os.WriteFile(schemaPath, outputSchema, 0o600); err != nil {
		return CodexResult{}, err
	}
	args := []string{"exec", "--json", "--output-schema", schemaPath, "-o", outputPath, "-s", "read-only", "-C", root}
	if strings.TrimSpace(cfg.Model) != "" {
		args = append(args, "--model", strings.TrimSpace(cfg.Model))
	}
	args = append(args, prompt)
	command := exec.CommandContext(commandCtx, capability.Path, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return CodexResult{}, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return CodexResult{}, err
	}
	if err := command.Start(); err != nil {
		return CodexResult{}, classifyCodexError(commandCtx, err, "")
	}
	result := CodexResult{Capability: capability, Events: []CodexEvent{}}
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 32*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			event := CodexEvent{Raw: json.RawMessage(line)}
			var envelope struct {
				Type   string `json:"type"`
				Error  string `json:"error"`
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(line), &envelope) == nil {
				event.Type, event.Error, event.Status = envelope.Type, envelope.Error, envelope.Status
			}
			result.Events = append(result.Events, event)
		}
		readErr <- scanner.Err()
	}()
	stderrData := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(io.LimitReader(stderr, 1<<20))
		stderrData <- data
	}()
	waitErr := command.Wait()
	errText := <-stderrData
	if commandCtx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return result, ErrExecutionCanceled
		}
		return result, ErrExecutionCanceled
	}
	if scanErr := <-readErr; scanErr != nil {
		return result, scanErr
	}
	if waitErr != nil {
		return result, classifyCodexError(commandCtx, waitErr, string(errText))
	}
	payload, err := os.ReadFile(outputPath)
	if err != nil {
		return result, fmt.Errorf("codex structured output missing: %w", err)
	}
	if !json.Valid(payload) {
		return result, errors.New("codex structured output is not valid JSON")
	}
	sum := sha256.Sum256(payload)
	result.Raw = append([]byte(nil), payload...)
	result.ContentHash = hex.EncodeToString(sum[:])
	return result, nil
}

func classifyCodexError(ctx context.Context, err error, stderr string) error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrExecutionCanceled
	}
	value := strings.ToLower(stderr + " " + err.Error())
	for _, term := range []string{"permission", "approval", "sandbox", "denied", "forbidden", "not allowed"} {
		if strings.Contains(value, term) {
			return fmt.Errorf("%w: %s", ErrExecutionDenied, strings.TrimSpace(stderr))
		}
	}
	return err
}

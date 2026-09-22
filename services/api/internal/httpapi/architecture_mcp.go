package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/architecture"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
)

// mcpArchitectureAvailable is deliberately backed by registered, scoped
// agents. A configured MCP path with no eligible worker is unavailable and
// therefore may fall through to the configured provider in auto mode.
func (s *Server) mcpArchitectureAvailable(ctx context.Context, project model.Project) bool {
	if s.Store == nil || !project.AllowRemoteCode {
		return false
	}
	agents, err := s.Store.ListArchitectureAgents(ctx)
	if err != nil {
		return false
	}
	for _, agent := range agents {
		if agent.Status == "online" && s.Store.ArchitectureAgentCanAnalyze(agent, project.ID) {
			return true
		}
	}
	return false
}

// runMCPArchitectureAgent creates a durable hand-off task, waits for a
// registered MCP worker to claim it, keeps the lease alive, and reads the
// worker's submitted result. The worker result is persisted atomically by
// SubmitArchitectureAgentResult; mcp tasks deliberately do not publish the
// project current pointer until the API generation path has rendered and
// validated all artifacts.
func (s *Server) runMCPArchitectureAgent(ctx context.Context, request model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error) {
	if s.Store == nil {
		return model.ArchitectureAnalysis{}, architecture.ErrNoArchitectureExecutor
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !request.Project.AllowRemoteCode {
		return model.ArchitectureAnalysis{}, architecture.ErrNoArchitectureExecutor
	}
	task, err := s.Store.CreateArchitectureTask(ctx, request.Project.ID, "mcp", request)
	if errors.Is(err, storage.ErrProjectTaskActive) {
		return model.ArchitectureAnalysis{}, fmt.Errorf("MCP architecture task is already active: %w", err)
	}
	if err != nil {
		return model.ArchitectureAnalysis{}, err
	}
	// The queued task is visible to the dedicated agent endpoint. It is not
	// claimed here: claiming is the worker's proof that it received the task.
	defer func() {
		if taskItem, getErr := s.Store.GetArchitectureTask(context.Background(), task.ID, false); getErr == nil && (taskItem.Status == "queued" || taskItem.Status == "running") {
			_, _ = s.Store.CancelArchitectureTask(context.Background(), task.ID)
		}
	}()

	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	heartbeat := time.NewTicker(2 * time.Minute)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			_, _ = s.Store.CancelArchitectureTask(context.Background(), task.ID)
			return model.ArchitectureAnalysis{}, architecture.ErrExecutionCanceled
		case <-heartbeat.C:
			current, getErr := s.Store.GetArchitectureTask(ctx, task.ID, true)
			if getErr != nil {
				return model.ArchitectureAnalysis{}, getErr
			}
			if strings.HasPrefix(current.LeaseOwner, "agent:") && current.LeaseToken != "" {
				if renewErr := s.Store.RenewArchitectureTask(ctx, task.ID, current.LeaseOwner, current.LeaseToken, 10*time.Minute); renewErr != nil {
					return model.ArchitectureAnalysis{}, fmt.Errorf("MCP agent lease heartbeat failed: %w", renewErr)
				}
			}
		case <-poll.C:
			current, getErr := s.Store.GetArchitectureTask(ctx, task.ID, true)
			if getErr != nil {
				return model.ArchitectureAnalysis{}, getErr
			}
			switch current.Status {
			case "completed":
				if current.SnapshotID == "" {
					return model.ArchitectureAnalysis{}, errors.New("MCP agent completed without a snapshot")
				}
				analysis, analysisErr := s.Store.GetArchitectureAnalysis(ctx, request.Project.ID, current.SnapshotID)
				if analysisErr != nil {
					return model.ArchitectureAnalysis{}, fmt.Errorf("MCP agent result read failed: %w", analysisErr)
				}
				return analysis, nil
			case "failed":
				message := current.Message
				if message == "" {
					message = "MCP agent failed architecture analysis"
				}
				return model.ArchitectureAnalysis{}, errors.New(message)
			case "cancelled":
				return model.ArchitectureAnalysis{}, architecture.ErrExecutionCanceled
			}
		}
	}
}

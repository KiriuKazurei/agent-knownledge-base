package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/architecture"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/secrets"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (s *Server) generateArchitectureProject(c *gin.Context) {
	ctx := operationContext(c)
	project, err := s.Store.GetProject(ctx, c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "project_not_found", "Architecture project not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	task, err := s.Store.CreateArchitectureTask(ctx, project.ID, "analysis")
	if errors.Is(err, storage.ErrProjectTaskActive) {
		s.problem(c, http.StatusConflict, "project_analysis_active", err.Error(), false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "task_create_failed", err.Error(), true)
		return
	}
	go s.runArchitectureAnalysis(s.lifecycleContext(), task.ID, project)
	c.JSON(http.StatusAccepted, task)
}

func (s *Server) runArchitectureAnalysis(parent context.Context, taskID string, project model.Project) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, analysisTimeout(project))
	defer cancel()
	owner := "api-" + uuid.NewString()
	task, err := s.Store.ClaimArchitectureTask(ctx, taskID, owner, analysisTimeout(project)+5*time.Minute)
	if err != nil {
		if ctx.Err() != nil {
			persistCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = s.Store.CancelArchitectureTask(persistCtx, taskID)
			stop()
		}
		if s.Logger != nil {
			s.Logger.Warn("architecture analysis task claim failed", "taskId", taskID, "error", err)
		}
		return
	}
	analysisCtx, stop := context.WithCancel(ctx)
	defer stop()
	go s.watchArchitectureTask(analysisCtx, taskID, stop)
	snapshot, err := s.Store.GetLatestArchitectureSnapshot(analysisCtx, project.ID)
	if err != nil {
		s.failArchitectureTask(ctx, taskID, owner, task.LeaseToken, project.ID, err)
		return
	}
	if project.CurrentAnalysisID != "" && project.CurrentAnalysisID == snapshot.ID {
		if _, submitErr := s.Store.SubmitArchitectureTask(ctx, taskID, owner, task.LeaseToken, "completed", snapshot.ID, "Architecture is unchanged"); submitErr != nil {
			s.failArchitectureTask(ctx, taskID, owner, task.LeaseToken, project.ID, submitErr)
		}
		return
	}
	previous, _ := s.Store.GetArchitectureSnapshot(analysisCtx, project.ID, project.CurrentAnalysisID)
	request := model.ArchitectureAnalysisRequest{Project: project, Snapshot: snapshot, Incremental: architectureIncremental(previous, snapshot), Prompt: "Analyze this project snapshot and return kah.architecture.analysis.v1 JSON. Facts and inferences must use exact evidence IDs and file line ranges; distinguish code facts from inferences and mark unknown deployment relationships."}
	run, err := s.ArchitectureChain.Run(analysisCtx, request)
	if err != nil {
		s.failArchitectureTask(ctx, taskID, owner, task.LeaseToken, project.ID, err)
		return
	}
	analysis := run.Analysis
	analysis.ProjectID, analysis.SnapshotID = project.ID, snapshot.ID
	if analysis.Incremental.BaseSnapshotID == "" {
		analysis.Incremental = request.Incremental
	}
	if err := s.Store.SaveArchitectureAnalysis(analysisCtx, analysis); err != nil {
		s.failArchitectureTask(ctx, taskID, owner, task.LeaseToken, project.ID, err)
		return
	}
	ir := architecture.BuildArchitectureIR(project, snapshot, analysis)
	if err := architecture.ValidateArchitectureIR(ir); err != nil {
		s.failArchitectureTask(ctx, taskID, owner, task.LeaseToken, project.ID, err)
		return
	}
	artifacts, err := s.architectureArtifacts(analysisCtx, project, snapshot, analysis, ir)
	if err != nil {
		s.failArchitectureTask(ctx, taskID, owner, task.LeaseToken, project.ID, err)
		return
	}
	if _, err := s.Store.SaveArchitectureArtifacts(analysisCtx, project.ID, snapshot.ID, artifacts); err != nil {
		s.failArchitectureTask(ctx, taskID, owner, task.LeaseToken, project.ID, err)
		return
	}
	if analysisCtx.Err() != nil {
		s.failArchitectureTask(ctx, taskID, owner, task.LeaseToken, project.ID, analysisCtx.Err())
		return
	}
	if err := s.Store.CommitArchitectureAnalysisTask(ctx, taskID, owner, task.LeaseToken, snapshot.ID); err != nil {
		s.failArchitectureTask(ctx, taskID, owner, task.LeaseToken, project.ID, err)
		return
	}
	// The architecture snapshot is current only after the durable commit. A
	// retrieval rebuild failure leaves the previous generation serving and is
	// recorded as stale for an explicit retry.
	s.refreshUnifiedLibrary(s.lifecycleContext(), project.LibraryID)
}

func analysisTimeout(project model.Project) time.Duration {
	if project.Execution.TimeoutSeconds > 0 {
		return time.Duration(project.Execution.TimeoutSeconds) * time.Second
	}
	return 30 * time.Minute
}

func (s *Server) watchArchitectureTask(ctx context.Context, taskID string, cancel context.CancelFunc) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var status string
			var requested int
			if err := s.Store.DB.QueryRowContext(ctx, "SELECT status,cancel_requested FROM architecture_tasks WHERE id=?", taskID).Scan(&status, &requested); err != nil || status != "running" || requested != 0 {
				cancel()
				return
			}
		}
	}
}

func (s *Server) failArchitectureTask(ctx context.Context, taskID, owner, token, projectID string, cause error) {
	if cause == nil {
		cause = errors.New("architecture generation failed")
	}
	// Once the worker context has been cancelled, the context passed to the
	// request handler is no longer usable for the terminal write. Persist the
	// cancellation through a short-lived process context so shutdown or user
	// cancellation cannot leave a leased analysis task running forever.
	persistCtx := ctx
	if isArchitectureCancellation(ctx, cause) {
		var stop context.CancelFunc
		persistCtx, stop = context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
	}
	currentID := ""
	if project, err := s.Store.GetProject(persistCtx, projectID); err == nil {
		currentID = project.CurrentAnalysisID
	}
	_ = s.Store.SetArchitectureAnalysisState(persistCtx, projectID, "stale", cause.Error(), currentID)
	if isArchitectureCancellation(ctx, cause) {
		if _, err := s.Store.CancelArchitectureTask(persistCtx, taskID); err == nil || errors.Is(err, sql.ErrNoRows) {
			return
		} else if s.Logger != nil {
			s.Logger.Warn("architecture analysis cancellation could not be persisted", "taskId", taskID, "error", err)
		}
	}
	if _, err := s.Store.SubmitArchitectureTask(persistCtx, taskID, owner, token, "failed", "", cause.Error()); err != nil && s.Logger != nil {
		s.Logger.Warn("architecture analysis failure could not be persisted", "taskId", taskID, "error", err)
	}
}

func isArchitectureCancellation(ctx context.Context, cause error) bool {
	return (ctx != nil && ctx.Err() != nil) || errors.Is(cause, architecture.ErrExecutionCanceled) || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded)
}

func architectureIncremental(previous, current model.ArchitectureSnapshot) model.AnalysisIncremental {
	result := model.AnalysisIncremental{BaseSnapshotID: previous.ID, Added: []string{}, Changed: []string{}, Deleted: []string{}, Unchanged: []string{}}
	if previous.ID == "" {
		return result
	}
	oldFiles, newFiles := map[string]model.ArchitectureFile{}, map[string]model.ArchitectureFile{}
	for _, item := range previous.Files {
		oldFiles[item.Path] = item
	}
	for _, item := range current.Files {
		newFiles[item.Path] = item
	}
	for path, item := range newFiles {
		old, ok := oldFiles[path]
		if !ok {
			result.Added = append(result.Added, path)
		} else if old.ContentHash != item.ContentHash {
			result.Changed = append(result.Changed, path)
		} else {
			result.Unchanged = append(result.Unchanged, path)
		}
	}
	for path := range oldFiles {
		if _, ok := newFiles[path]; !ok {
			result.Deleted = append(result.Deleted, path)
		}
	}
	for _, values := range [][]string{result.Added, result.Changed, result.Deleted, result.Unchanged} {
		sortStrings(values)
	}
	return result
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func (s *Server) architectureArtifacts(ctx context.Context, project model.Project, snapshot model.ArchitectureSnapshot, analysis model.ArchitectureAnalysis, ir architecture.ArchitectureIR) ([]storage.ArchitectureArtifactInput, error) {
	document, err := architecture.RenderArchitectureMarkdown(project, snapshot, analysis, ir)
	if err != nil {
		return nil, err
	}
	jsonData, err := architecture.RenderArchitectureJSON(ir)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// The vendored Archify CLI is the sole producer of the HTML/SVG diagram.
	// The fallback only covers PNG when the optional local browser is absent;
	// its renderer and error are persisted so the UI cannot present it as
	// Archify output.
	archify, err := s.Archify.RenderArchitecture(ctx, project, snapshot, analysis, ir, filepath.Join(s.Store.DataRoot, "architecture", ".render"))
	if err != nil {
		return nil, err
	}
	return []storage.ArchitectureArtifactInput{
		{Kind: "document", FileName: "architecture.md", ContentType: "text/markdown; charset=utf-8", Renderer: "kah-architecture-document-v1", Data: document},
		{Kind: "diagram_json", FileName: "architecture.json", ContentType: "application/json", Renderer: architecture.ArchifyRenderer, Data: jsonData},
		{Kind: "html", FileName: "architecture.html", ContentType: "text/html; charset=utf-8", Renderer: architecture.ArchifySource + "@" + architecture.ArchifyCommit, Data: archify.HTML},
		{Kind: "svg", FileName: "architecture.svg", ContentType: "image/svg+xml", Renderer: architecture.ArchifySource + "@" + architecture.ArchifyCommit, Data: archify.SVG},
		{Kind: "png", FileName: "architecture.png", ContentType: "image/png", Renderer: archify.PNGRenderer, RendererError: archify.BrowserError, Data: archify.PNG},
	}, nil
}

func (s *Server) runConfiguredArchitectureProvider(ctx context.Context, request model.ArchitectureAnalysisRequest) (model.ArchitectureAnalysis, error) {
	if request.Project.Execution.ProviderID == "" {
		return model.ArchitectureAnalysis{}, architecture.ErrNoArchitectureExecutor
	}
	provider, err := s.Store.GetProvider(ctx, request.Project.Execution.ProviderID)
	if err != nil {
		return model.ArchitectureAnalysis{}, err
	}
	key, err := secrets.Get("provider:" + provider.ID)
	if err != nil {
		return model.ArchitectureAnalysis{}, err
	}
	var material strings.Builder
	for _, file := range request.Snapshot.Files {
		path := filepath.Join(request.Project.RootPath, filepath.FromSlash(file.Path))
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		if len(data) > 256*1024 {
			data = data[:256*1024]
		}
		fmt.Fprintf(&material, "\nFILE %s lines %d-%d hash %s\n%s\n", file.Path, file.LineStart, file.LineEnd, file.ContentHash, string(data))
		if material.Len() > 2*1024*1024 {
			break
		}
	}
	prompt := request.Prompt + "\n\nReturn only JSON matching kah.architecture.analysis.v1.\n" + material.String()
	text, err := s.Providers.Generate(ctx, provider, key, prompt, nil)
	if err != nil {
		return model.ArchitectureAnalysis{}, err
	}
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(text, "```"), "```json"))
	var result model.ArchitectureAnalysis
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return result, fmt.Errorf("provider architecture JSON: %w", err)
	}
	return result, nil
}

func (s *Server) getArchitectureDiff(c *gin.Context) {
	ctx := operationContext(c)
	projectID := c.Param("id")
	fromID, toID := c.Query("fromSnapshotId"), c.Param("snapshotId")
	to, err := s.Store.GetArchitectureSnapshot(ctx, projectID, toID)
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, 404, "snapshot_not_found", "Architecture snapshot not found", false)
		return
	}
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	if fromID == "" {
		snapshots, listErr := s.Store.ListArchitectureSnapshots(ctx, projectID)
		if listErr != nil || len(snapshots) < 2 {
			c.JSON(200, model.ArchitectureDiff{ProjectID: projectID, ToSnapshotID: toID, Added: []string{}, Changed: []string{}, Deleted: []string{}, Unchanged: []string{}})
			return
		}
		for _, item := range snapshots {
			if item.ID != toID {
				fromID = item.ID
				break
			}
		}
	}
	from, err := s.Store.GetArchitectureSnapshot(ctx, projectID, fromID)
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, 404, "snapshot_not_found", "Base snapshot not found", false)
		return
	}
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	incremental := architectureIncremental(from, to)
	result := model.ArchitectureDiff{ProjectID: projectID, FromSnapshotID: from.ID, ToSnapshotID: to.ID, Added: incremental.Added, Changed: incremental.Changed, Deleted: incremental.Deleted, Unchanged: incremental.Unchanged}
	if analysis, analysisErr := s.Store.GetArchitectureAnalysis(ctx, projectID, to.ID); analysisErr == nil {
		result.Analysis = &analysis
	}
	c.JSON(200, result)
}

func (s *Server) listArchitectureArtifacts(c *gin.Context) {
	items, err := s.Store.ListArchitectureArtifacts(operationContext(c), c.Param("id"), c.Param("snapshotId"))
	if err != nil {
		s.problem(c, 500, "artifact_list_failed", err.Error(), true)
		return
	}
	c.JSON(200, items)
}
func (s *Server) downloadArchitectureArtifact(c *gin.Context) {
	item, err := s.Store.GetArchitectureArtifact(operationContext(c), c.Param("id"), c.Param("snapshotId"), c.Param("kind"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, 404, "artifact_not_found", "Architecture artifact not found", false)
		return
	}
	if err != nil {
		s.problem(c, 500, "artifact_read_failed", err.Error(), true)
		return
	}
	data, err := s.Store.ReadArchitectureArtifact(item)
	if err != nil {
		s.problem(c, 500, "artifact_read_failed", err.Error(), true)
		return
	}
	c.Data(http.StatusOK, item.ContentType, data)
}

func (s *Server) registerArchitectureAgent(c *gin.Context) {
	var input model.ArchitectureAgentRegistration
	if !bind(c, &input) {
		return
	}
	credential, err := s.Store.RegisterArchitectureAgentCredential(operationContext(c), input)
	if err != nil {
		s.problem(c, 400, "agent_registration_failed", err.Error(), false)
		return
	}
	c.JSON(http.StatusCreated, credential)
}
func (s *Server) listArchitectureAgents(c *gin.Context) {
	items, err := s.Store.ListArchitectureAgents(operationContext(c))
	if err != nil {
		s.problem(c, 500, "agent_list_failed", err.Error(), true)
		return
	}
	c.JSON(200, items)
}

func (s *Server) architectureAgentAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
			s.problem(c, 401, "architecture_agent_auth_required", "Architecture agent bearer secret is required", false)
			c.Abort()
			return
		}
		agent, err := s.Store.AuthenticateArchitectureAgent(operationContext(c), strings.TrimSpace(header[len("Bearer "):]))
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, 401, "architecture_agent_auth_invalid", "Architecture agent credential is invalid", false)
			c.Abort()
			return
		}
		if err != nil {
			s.problem(c, 500, "agent_auth_failed", err.Error(), true)
			c.Abort()
			return
		}
		c.Set("architectureAgent", agent)
		c.Next()
	}
}

func (s *Server) architectureAgent(c *gin.Context) (model.ArchitectureAgent, bool) {
	value, ok := c.Get("architectureAgent")
	agent, valid := value.(model.ArchitectureAgent)
	if !ok || !valid {
		s.problem(c, 401, "architecture_agent_auth_required", "Architecture agent authentication is required", false)
		return agent, false
	}
	return agent, true
}
func (s *Server) claimArchitectureAgentTask(c *gin.Context) {
	agent, ok := s.architectureAgent(c)
	if !ok {
		return
	}
	var input struct {
		LeaseSeconds int `json:"leaseSeconds"`
	}
	if c.Request.ContentLength > 0 && !bind(c, &input) {
		return
	}
	lease := 10 * time.Minute
	if input.LeaseSeconds > 0 {
		lease = time.Duration(input.LeaseSeconds) * time.Second
	}
	task, err := s.Store.ClaimNextArchitectureTask(operationContext(c), agent, lease)
	if errors.Is(err, sql.ErrNoRows) {
		c.Status(http.StatusNoContent)
		return
	}
	if err != nil {
		s.problem(c, 409, "agent_claim_failed", err.Error(), false)
		return
	}
	c.JSON(200, model.ArchitectureAgentLease{AgentID: agent.ID, Task: task, LeaseToken: task.LeaseToken, LeaseExpires: *task.LeaseExpiresAt})
}
func (s *Server) heartbeatArchitectureAgentTask(c *gin.Context) {
	agent, ok := s.architectureAgent(c)
	if !ok {
		return
	}
	var input struct {
		TaskID     string `json:"taskId"`
		LeaseToken string `json:"leaseToken"`
	}
	if !bind(c, &input) {
		return
	}
	task, err := s.Store.GetArchitectureTask(operationContext(c), input.TaskID, true)
	if err != nil || task.LeaseOwner != "agent:"+agent.ID {
		s.problem(c, 409, "agent_lease_invalid", "Task lease is invalid", false)
		return
	}
	if err := s.Store.RenewArchitectureTask(operationContext(c), input.TaskID, "agent:"+agent.ID, input.LeaseToken, 10*time.Minute); err != nil {
		s.problem(c, 409, "agent_lease_expired", err.Error(), false)
		return
	}
	c.Status(204)
}
func (s *Server) submitArchitectureAgentResult(c *gin.Context) {
	agent, ok := s.architectureAgent(c)
	if !ok {
		return
	}
	var input struct {
		TaskID     string                              `json:"taskId"`
		Generation int64                               `json:"generation"`
		LeaseToken string                              `json:"leaseToken"`
		Analysis   model.ArchitectureAnalysis          `json:"analysis"`
		Artifacts  []storage.ArchitectureArtifactInput `json:"artifacts"`
	}
	if !bind(c, &input) {
		return
	}
	task, err := s.Store.GetArchitectureTask(operationContext(c), input.TaskID, true)
	if err != nil || !s.Store.ArchitectureAgentCanAnalyze(agent, task.ProjectID) {
		s.problem(c, 403, "agent_project_forbidden", "Agent is not authorized for this project", false)
		return
	}
	result, err := s.Store.SubmitArchitectureAgentResult(operationContext(c), input.TaskID, agent.ID, input.LeaseToken, input.Analysis, input.Artifacts, input.Generation)
	if errors.Is(err, storage.ErrTaskLeaseExpired) || errors.Is(err, storage.ErrTaskCancelled) {
		s.problem(c, 409, "agent_lease_expired", err.Error(), false)
		return
	}
	if err != nil {
		s.problem(c, 400, "agent_result_invalid", err.Error(), false)
		return
	}
	c.JSON(200, result)
}
func (s *Server) submitArchitectureAgentFailure(c *gin.Context) {
	agent, ok := s.architectureAgent(c)
	if !ok {
		return
	}
	var input struct {
		TaskID     string `json:"taskId"`
		LeaseToken string `json:"leaseToken"`
		Message    string `json:"message"`
	}
	if !bind(c, &input) {
		return
	}
	task, err := s.Store.GetArchitectureTask(operationContext(c), input.TaskID, true)
	if err != nil || !s.Store.ArchitectureAgentCanAnalyze(agent, task.ProjectID) {
		s.problem(c, 403, "agent_project_forbidden", "Agent is not authorized for this project", false)
		return
	}
	result, err := s.Store.SubmitArchitectureAgentFailure(operationContext(c), input.TaskID, agent.ID, input.LeaseToken, input.Message)
	if errors.Is(err, storage.ErrTaskLeaseExpired) || errors.Is(err, storage.ErrTaskCancelled) {
		s.problem(c, 409, "agent_lease_expired", err.Error(), false)
		return
	}
	if err != nil {
		s.problem(c, 400, "agent_failure_invalid", err.Error(), false)
		return
	}
	c.JSON(200, result)
}

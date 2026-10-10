package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/architecture"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (s *Server) listProjects(c *gin.Context) {
	items, err := s.Store.ListProjects(operationContext(c), c.Query("libraryId"))
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) createProject(c *gin.Context) {
	var input model.Project
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.CreateProject(operationContext(c), input)
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "library_not_found", "Knowledge library not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusBadRequest, "project_create_failed", err.Error(), false)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "architecture_project_created", item.ID, map[string]any{"libraryId": item.LibraryID, "rootPath": item.RootPath})
	c.JSON(http.StatusCreated, item)
}

func (s *Server) getProject(c *gin.Context) {
	item, err := s.Store.GetProject(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "project_not_found", "Architecture project not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) updateProject(c *gin.Context) {
	var input struct {
		Name              *string                       `json:"name"`
		Description       *string                       `json:"description"`
		RootPath          *string                       `json:"rootPath"`
		ExcludePatterns   *[]string                     `json:"excludePatterns"`
		ScanPeriodSeconds *int                          `json:"scanPeriodSeconds"`
		ScheduleKind      *string                       `json:"scheduleKind"`
		ScheduleAnchor    *string                       `json:"scheduleAnchor"`
		AllowRemoteCode   *bool                         `json:"allowRemoteCode"`
		Execution         *model.ProjectExecutionConfig `json:"execution"`
	}
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.UpdateProject(operationContext(c), c.Param("id"), storage.ProjectPatch{Name: input.Name, Description: input.Description, RootPath: input.RootPath, ExcludePatterns: input.ExcludePatterns, ScanPeriodSeconds: input.ScanPeriodSeconds, ScheduleKind: input.ScheduleKind, ScheduleAnchor: input.ScheduleAnchor, AllowRemoteCode: input.AllowRemoteCode, Execution: input.Execution})
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "project_not_found", "Architecture project not found", false)
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "active") {
			status = http.StatusConflict
		}
		s.problem(c, status, "project_update_failed", err.Error(), false)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "architecture_project_updated", item.ID, nil)
	c.JSON(http.StatusOK, item)
}

func (s *Server) deleteProject(c *gin.Context) {
	project, getErr := s.Store.GetProject(operationContext(c), c.Param("id"))
	if errors.Is(getErr, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "project_not_found", "Architecture project not found", false)
		return
	}
	if getErr != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", getErr.Error(), true)
		return
	}
	err := s.Store.DeleteProject(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "project_not_found", "Architecture project not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "architecture_project_trashed", c.Param("id"), nil)
	s.refreshUnifiedLibrary(s.lifecycleContext(), project.LibraryID)
	c.Status(http.StatusNoContent)
}

func (s *Server) scanProject(c *gin.Context) {
	project, err := s.Store.GetProject(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "project_not_found", "Architecture project not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	task, err := s.Store.CreateArchitectureTask(operationContext(c), project.ID, "scan")
	if errors.Is(err, storage.ErrProjectTaskActive) {
		s.problem(c, http.StatusConflict, "project_scan_active", err.Error(), false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "task_create_failed", err.Error(), true)
		return
	}
	go s.runArchitectureScan(s.lifecycleContext(), task.ID, project)
	c.JSON(http.StatusAccepted, task)
}

func (s *Server) listArchitectureTasks(c *gin.Context) {
	if _, err := s.Store.GetProject(operationContext(c), c.Param("id")); errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "project_not_found", "Architecture project not found", false)
		return
	}
	items, err := s.Store.ListArchitectureTasks(operationContext(c), c.Param("id"))
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) listArchitectureSnapshots(c *gin.Context) {
	if _, err := s.Store.GetProject(operationContext(c), c.Param("id")); errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "project_not_found", "Architecture project not found", false)
		return
	}
	items, err := s.Store.ListArchitectureSnapshots(operationContext(c), c.Param("id"))
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) getArchitectureSnapshot(c *gin.Context) {
	item, err := s.Store.GetArchitectureSnapshot(operationContext(c), c.Param("id"), c.Param("snapshotId"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "snapshot_not_found", "Architecture snapshot not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) saveArchitectureAnalysis(c *gin.Context) {
	var input model.ArchitectureAnalysis
	if !bind(c, &input) {
		return
	}
	input.ProjectID, input.SnapshotID = c.Param("id"), c.Param("snapshotId")
	if _, err := s.Store.GetArchitectureSnapshot(operationContext(c), input.ProjectID, input.SnapshotID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "snapshot_not_found", "Architecture snapshot not found", false)
		} else {
			s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		}
		return
	}
	if err := s.Store.SaveArchitectureAnalysis(operationContext(c), input); err != nil {
		s.problem(c, http.StatusBadRequest, "analysis_invalid", err.Error(), false)
		return
	}
	c.JSON(http.StatusCreated, input)
}

func (s *Server) getArchitectureAnalysis(c *gin.Context) {
	item, err := s.Store.GetArchitectureAnalysis(operationContext(c), c.Param("id"), c.Param("snapshotId"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "analysis_not_found", "Architecture analysis not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) cancelArchitectureTask(c *gin.Context) {
	item, err := s.Store.CancelArchitectureTask(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "task_not_found", "Architecture task not found or is terminal", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusConflict, "task_cancel_failed", err.Error(), false)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) runArchitectureScan(parent context.Context, taskID string, project model.Project) {
	s.runArchitectureScanMode(parent, taskID, project, false)
}

func (s *Server) runArchitectureScanAndGenerate(parent context.Context, taskID string, project model.Project) {
	s.runArchitectureScanMode(parent, taskID, project, true)
}

func (s *Server) runArchitectureScanMode(parent context.Context, taskID string, project model.Project, generate bool) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	owner := "api-" + uuid.NewString()
	task, err := s.Store.ClaimArchitectureTask(ctx, taskID, owner, 35*time.Minute)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Warn("architecture task claim failed", "taskId", taskID, "error", err)
		}
		return
	}
	scanCtx, stopScan := context.WithCancel(ctx)
	defer stopScan()
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-scanCtx.Done():
				return
			case <-ticker.C:
				var status string
				var cancelRequested int
				if queryErr := s.Store.DB.QueryRowContext(scanCtx, "SELECT status,cancel_requested FROM architecture_tasks WHERE id=?", taskID).Scan(&status, &cancelRequested); queryErr != nil || status != "running" || cancelRequested != 0 {
					stopScan()
					return
				}
			}
		}
	}()
	snapshot, err := architecture.Scan(scanCtx, architecture.ScanConfig{ProjectID: project.ID, RootPath: project.RootPath, ExcludePatterns: project.ExcludePatterns})
	if err != nil {
		if ctx.Err() != nil || scanCtx.Err() != nil {
			return
		}
		if _, submitErr := s.Store.SubmitArchitectureTask(ctx, taskID, owner, task.LeaseToken, "failed", "", err.Error()); submitErr != nil && s.Logger != nil {
			s.Logger.Warn("architecture task failure could not be committed", "taskId", taskID, "error", submitErr)
		}
		return
	}
	if ctx.Err() != nil || scanCtx.Err() != nil {
		return
	}
	if _, _, err = s.Store.SaveArchitectureSnapshotForTask(ctx, taskID, owner, task.LeaseToken, snapshot); err != nil {
		if s.Logger != nil {
			s.Logger.Warn("architecture snapshot commit failed", "taskId", taskID, "error", err)
		}
		return
	}
	if generate && project.Execution.Executor != "none" && project.Execution.Executor != "" {
		if analysisTask, createErr := s.Store.CreateArchitectureTask(ctx, project.ID, "analysis"); createErr == nil {
			go s.runArchitectureAnalysis(s.lifecycleContext(), analysisTask.ID, project)
		}
	}
}

// StartArchitectureScheduler runs only while the API process is alive. A
// due schedule remains durable until its task is successfully enqueued, so a
// restart produces one merged catch-up run and never concurrent work for the
// same project.
func (s *Server) StartArchitectureScheduler(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.SetLifecycleContext(ctx)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		_ = s.architectureScheduleTick(ctx, time.Now().UTC())
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_ = s.architectureScheduleTick(ctx, now.UTC())
			}
		}
	}()
}

func (s *Server) architectureScheduleTick(ctx context.Context, now time.Time) error {
	if !s.beginRuntimeOperation() {
		return nil
	}
	defer s.endRuntimeOperation()
	projects, err := s.Store.ListDueArchitectureProjects(ctx, now)
	if err != nil {
		return err
	}
	for _, due := range projects {
		project, projectErr := s.Store.GetProject(ctx, due.ProjectID)
		if projectErr != nil {
			continue
		}
		task, taskErr := s.Store.CreateArchitectureTask(ctx, project.ID, "scan")
		if taskErr != nil {
			if errors.Is(taskErr, storage.ErrProjectTaskActive) {
				continue
			}
			if s.Logger != nil {
				s.Logger.Warn("scheduled architecture task could not be created", "projectId", project.ID, "error", taskErr)
			}
			continue
		}
		if markErr := s.Store.MarkArchitectureSchedule(ctx, project.ID, due.NextAt, now); markErr != nil {
			_, _ = s.Store.CancelArchitectureTask(ctx, task.ID)
			continue
		}
		go s.runArchitectureScanAndGenerate(s.lifecycleContext(), task.ID, project)
	}
	return nil
}

// ResumeArchitectureTasks restores both scan and analysis tasks. Lease tokens
// from the previous process are cleared during storage recovery, so an old
// executor cannot submit after restart.
func (s *Server) ResumeArchitectureTasks(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.SetLifecycleContext(ctx)
	if err := s.Store.RecoverArchitectureTasks(ctx); err != nil {
		return err
	}
	items, err := s.Store.ListArchitectureTasks(ctx, "")
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Status != "queued" || (item.Kind != "scan" && item.Kind != "analysis") {
			continue
		}
		project, projectErr := s.Store.GetProject(ctx, item.ProjectID)
		if projectErr != nil {
			continue
		}
		if item.Kind == "scan" {
			go s.runArchitectureScan(ctx, item.ID, project)
		} else {
			go s.runArchitectureAnalysis(ctx, item.ID, project)
		}
	}
	return nil
}

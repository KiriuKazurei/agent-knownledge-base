package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/google/uuid"
)

var (
	ErrProjectTaskActive = errors.New("an architecture task is already active for this project")
	ErrTaskLeaseExpired  = errors.New("architecture task lease is expired or invalid")
	ErrTaskCancelled     = errors.New("architecture task was cancelled")
)

func normalizeProjectExcludes(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(filepath.ToSlash(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) >= 200 {
			break
		}
	}
	return result
}

func normalizeExecution(value model.ProjectExecutionConfig) model.ProjectExecutionConfig {
	value.Executor = strings.TrimSpace(value.Executor)
	if value.Executor == "" {
		value.Executor = "none"
	}
	allowed := map[string]bool{"none": true, "auto": true, "codex": true, "mcp": true, "provider": true}
	if !allowed[value.Executor] {
		value.Executor = "none"
	}
	value.ProviderID = strings.TrimSpace(value.ProviderID)
	value.CodexPath = strings.TrimSpace(value.CodexPath)
	value.CodexModel = strings.TrimSpace(value.CodexModel)
	if value.TimeoutSeconds < 0 || value.TimeoutSeconds > 24*60*60 {
		value.TimeoutSeconds = 0
	}
	// Stage 1 never dispatches an external process.  The flag is retained as
	// declarative project policy for a later execution stage.
	return value
}

func normalizeSchedule(kind string, interval int, anchor string) (string, int, string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "manual"
	}
	switch kind {
	case "manual":
		return kind, 0, strings.TrimSpace(anchor), nil
	case "daily", "weekly":
		if interval != 0 {
			return "", 0, "", errors.New("scanPeriodSeconds is only valid for interval schedules")
		}
		if strings.TrimSpace(anchor) == "" {
			return "", 0, "", errors.New("scheduleAnchor is required for daily and weekly schedules")
		}
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(anchor)); err != nil {
			return "", 0, "", errors.New("scheduleAnchor must be an RFC3339 timestamp")
		}
		return kind, 0, strings.TrimSpace(anchor), nil
	case "interval", "fixed":
		if interval < 60 || interval > 365*24*60*60 {
			return "", 0, "", errors.New("scanPeriodSeconds must be between 60 and 31536000 for interval schedules")
		}
		return "interval", interval, strings.TrimSpace(anchor), nil
	default:
		return "", 0, "", errors.New("scheduleKind must be manual, daily, weekly, or interval")
	}
}

func scanProject(row rowScanner, includeDeleted bool) (model.Project, error) {
	var item model.Project
	var excludes, execution, created, updated, analysisStatus, analysisError, currentAnalysisID, scheduleAnchor, scheduleNextAt string
	var scheduleKind string
	var remote, changed sql.NullInt64
	if err := row.Scan(&item.ID, &item.LibraryID, &item.Name, &item.Description, &item.RootPath, &excludes, &item.ScanPeriodSeconds, &scheduleKind, &scheduleAnchor, &scheduleNextAt, &remote, &execution, &item.LastSnapshotID, &item.LastSnapshotHash, &changed, &analysisStatus, &analysisError, &currentAnalysisID, &created, &updated); err != nil {
		return item, err
	}
	_ = json.Unmarshal([]byte(excludes), &item.ExcludePatterns)
	if item.ExcludePatterns == nil {
		item.ExcludePatterns = []string{}
	}
	if err := json.Unmarshal([]byte(execution), &item.Execution); err != nil {
		item.Execution = normalizeExecution(model.ProjectExecutionConfig{})
	}
	item.Execution = normalizeExecution(item.Execution)
	item.ScheduleKind, item.ScheduleAnchor = scheduleKind, scheduleAnchor
	if scheduleNextAt != "" {
		value := parseTime(scheduleNextAt)
		item.ScheduleNextAt = &value
	}
	item.AllowRemoteCode = remote.Valid && remote.Int64 == 1
	if changed.Valid {
		value := changed.Int64 == 1
		item.LastScanChanged = &value
	}
	item.AnalysisStatus, item.AnalysisError, item.CurrentAnalysisID = analysisStatus, analysisError, currentAnalysisID
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	if !includeDeleted {
		item.DeletedAt = nil
	}
	return item, nil
}

func (s *Store) CreateProject(ctx context.Context, project model.Project) (model.Project, error) {
	project.Name = strings.TrimSpace(project.Name)
	project.Description = strings.TrimSpace(project.Description)
	project.RootPath = filepath.Clean(strings.TrimSpace(project.RootPath))
	if project.Name == "" || len([]rune(project.Name)) > 160 {
		return model.Project{}, errors.New("project name must contain 1-160 characters")
	}
	if project.RootPath == "" || project.RootPath == "." {
		return model.Project{}, errors.New("rootPath is required")
	}
	absolute, err := filepath.Abs(project.RootPath)
	if err != nil {
		return model.Project{}, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return model.Project{}, err
	}
	if !info.IsDir() {
		return model.Project{}, errors.New("rootPath must be a directory")
	}
	var libraryExists int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM libraries WHERE id=? AND deleted_at IS NULL", project.LibraryID).Scan(&libraryExists); err != nil {
		return model.Project{}, err
	}
	if libraryExists == 0 {
		return model.Project{}, sql.ErrNoRows
	}
	project.ID = uuid.NewString()
	project.RootPath = absolute
	project.ExcludePatterns = normalizeProjectExcludes(project.ExcludePatterns)
	project.Execution = normalizeExecution(project.Execution)
	var scheduleErr error
	project.ScheduleKind, project.ScanPeriodSeconds, project.ScheduleAnchor, scheduleErr = normalizeSchedule(project.ScheduleKind, project.ScanPeriodSeconds, project.ScheduleAnchor)
	if scheduleErr != nil {
		return model.Project{}, scheduleErr
	}
	created, stamp := now()
	excludes, _ := json.Marshal(project.ExcludePatterns)
	execution, _ := json.Marshal(project.Execution)
	nextAt := ""
	if project.ScheduleKind != "manual" {
		nextAt = scheduleInitialNext(project.ScheduleKind, project.ScheduleAnchor, created).Format(time.RFC3339Nano)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO architecture_projects(id,library_id,name,description,root_path,exclude_patterns_json,scan_period_seconds,schedule_kind,schedule_anchor,schedule_next_at,allow_remote_code,execution_json,last_snapshot_id,last_snapshot_hash,analysis_status,analysis_error,current_analysis_snapshot_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, project.ID, project.LibraryID, project.Name, project.Description, project.RootPath, string(excludes), project.ScanPeriodSeconds, project.ScheduleKind, project.ScheduleAnchor, nextAt, boolInt(project.AllowRemoteCode), string(execution), "", "", "", "", "", stamp, stamp)
	if err != nil {
		return model.Project{}, err
	}
	project.CreatedAt, project.UpdatedAt = created, created
	if nextAt != "" {
		value := parseTime(nextAt)
		project.ScheduleNextAt = &value
	}
	project.ExcludePatterns = append([]string(nil), project.ExcludePatterns...)
	return project, nil
}

func (s *Store) GetProject(ctx context.Context, id string) (model.Project, error) {
	return scanProject(s.DB.QueryRowContext(ctx, `SELECT p.id,p.library_id,p.name,p.description,p.root_path,p.exclude_patterns_json,p.scan_period_seconds,p.schedule_kind,p.schedule_anchor,p.schedule_next_at,p.allow_remote_code,p.execution_json,p.last_snapshot_id,p.last_snapshot_hash,p.last_scan_changed,p.analysis_status,p.analysis_error,p.current_analysis_snapshot_id,p.created_at,p.updated_at FROM architecture_projects p JOIN libraries l ON l.id=p.library_id WHERE p.id=? AND p.deleted_at IS NULL AND l.deleted_at IS NULL`, id), false)
}

func (s *Store) ListProjects(ctx context.Context, libraryID string) ([]model.Project, error) {
	query := `SELECT p.id,p.library_id,p.name,p.description,p.root_path,p.exclude_patterns_json,p.scan_period_seconds,p.schedule_kind,p.schedule_anchor,p.schedule_next_at,p.allow_remote_code,p.execution_json,p.last_snapshot_id,p.last_snapshot_hash,p.last_scan_changed,p.analysis_status,p.analysis_error,p.current_analysis_snapshot_id,p.created_at,p.updated_at FROM architecture_projects p JOIN libraries l ON l.id=p.library_id WHERE p.deleted_at IS NULL AND l.deleted_at IS NULL`
	args := []any{}
	if strings.TrimSpace(libraryID) != "" {
		query += " AND p.library_id=?"
		args = append(args, strings.TrimSpace(libraryID))
	}
	query += " ORDER BY lower(p.name),p.created_at"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Project{}
	for rows.Next() {
		item, scanErr := scanProject(rows, false)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type ProjectPatch struct {
	Name              *string
	Description       *string
	RootPath          *string
	ExcludePatterns   *[]string
	ScanPeriodSeconds *int
	ScheduleKind      *string
	ScheduleAnchor    *string
	AllowRemoteCode   *bool
	Execution         *model.ProjectExecutionConfig
}

func (s *Store) UpdateProject(ctx context.Context, id string, patch ProjectPatch) (model.Project, error) {
	item, err := s.GetProject(ctx, id)
	if err != nil {
		return item, err
	}
	if patch.RootPath != nil || patch.ExcludePatterns != nil || patch.ScanPeriodSeconds != nil || patch.ScheduleKind != nil || patch.ScheduleAnchor != nil || patch.AllowRemoteCode != nil || patch.Execution != nil {
		var active int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM architecture_tasks WHERE project_id=? AND status IN ('queued','running','cancellation_requested')`, id).Scan(&active); err != nil {
			return item, err
		}
		if active > 0 {
			return item, errors.New("project scan configuration cannot change while an architecture task is active")
		}
	}
	if patch.Name != nil {
		item.Name = strings.TrimSpace(*patch.Name)
		if item.Name == "" || len([]rune(item.Name)) > 160 {
			return item, errors.New("project name must contain 1-160 characters")
		}
	}
	if patch.Description != nil {
		item.Description = strings.TrimSpace(*patch.Description)
		if len([]rune(item.Description)) > 4000 {
			return item, errors.New("project description must be at most 4000 characters")
		}
	}
	if patch.RootPath != nil {
		root, rootErr := filepath.Abs(filepath.Clean(strings.TrimSpace(*patch.RootPath)))
		if rootErr != nil {
			return item, rootErr
		}
		info, statErr := os.Stat(root)
		if statErr != nil {
			return item, statErr
		}
		if !info.IsDir() {
			return item, errors.New("rootPath must be a directory")
		}
		item.RootPath = root
	}
	if patch.ExcludePatterns != nil {
		item.ExcludePatterns = normalizeProjectExcludes(*patch.ExcludePatterns)
	}
	if patch.ScanPeriodSeconds != nil {
		item.ScanPeriodSeconds = *patch.ScanPeriodSeconds
	}
	if patch.ScheduleKind != nil {
		item.ScheduleKind = *patch.ScheduleKind
	}
	if patch.ScheduleAnchor != nil {
		item.ScheduleAnchor = *patch.ScheduleAnchor
	}
	item.ScheduleKind, item.ScanPeriodSeconds, item.ScheduleAnchor, err = normalizeSchedule(item.ScheduleKind, item.ScanPeriodSeconds, item.ScheduleAnchor)
	if err != nil {
		return item, err
	}
	if patch.AllowRemoteCode != nil {
		item.AllowRemoteCode = *patch.AllowRemoteCode
	}
	if patch.Execution != nil {
		item.Execution = normalizeExecution(*patch.Execution)
	}
	excludes, _ := json.Marshal(item.ExcludePatterns)
	execution, _ := json.Marshal(item.Execution)
	_, stamp := now()
	nextAt := ""
	if item.ScheduleKind != "manual" {
		nextAt = scheduleInitialNext(item.ScheduleKind, item.ScheduleAnchor, time.Now().UTC()).Format(time.RFC3339Nano)
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE architecture_projects SET name=?,description=?,root_path=?,exclude_patterns_json=?,scan_period_seconds=?,schedule_kind=?,schedule_anchor=?,schedule_next_at=?,allow_remote_code=?,execution_json=?,updated_at=? WHERE id=? AND deleted_at IS NULL`, item.Name, item.Description, item.RootPath, string(excludes), item.ScanPeriodSeconds, item.ScheduleKind, item.ScheduleAnchor, nextAt, boolInt(item.AllowRemoteCode), string(execution), stamp, id)
	if err != nil {
		return item, err
	}
	item.UpdatedAt = parseTime(stamp)
	if nextAt != "" {
		value := parseTime(nextAt)
		item.ScheduleNextAt = &value
	} else {
		item.ScheduleNextAt = nil
	}
	return item, nil
}

func (s *Store) DeleteProject(ctx context.Context, id string) error {
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, `UPDATE architecture_projects SET deleted_at=?,updated_at=? WHERE id=? AND deleted_at IS NULL`, stamp, stamp, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE architecture_tasks SET cancel_requested=1,status=CASE WHEN status='queued' THEN 'cancelled' ELSE status END,message='Project deleted',updated_at=? WHERE project_id=? AND status IN ('queued','running','cancellation_requested')`, stamp, id)
	return err
}

func scanArchitectureTask(row rowScanner, includeLease bool) (model.ArchitectureTask, error) {
	var item model.ArchitectureTask
	var inputJSON, leaseExpiry, created, updated sql.NullString
	var cancelled int
	var owner, token string
	if err := row.Scan(&item.ID, &item.ProjectID, &item.Kind, &item.Status, &item.Generation, &inputJSON, &item.SnapshotID, &owner, &token, &leaseExpiry, &cancelled, &item.ErrorCode, &item.Message, &created, &updated); err != nil {
		return item, err
	}
	if inputJSON.Valid && strings.TrimSpace(inputJSON.String) != "" && inputJSON.String != "{}" {
		var input model.ArchitectureAnalysisRequest
		if json.Unmarshal([]byte(inputJSON.String), &input) == nil {
			item.Input = &input
		}
	}
	if includeLease {
		item.LeaseOwner, item.LeaseToken = owner, token
		if leaseExpiry.Valid && leaseExpiry.String != "" {
			value := parseTime(leaseExpiry.String)
			item.LeaseExpiresAt = &value
		}
	}
	item.CancelRequested = cancelled == 1
	item.CreatedAt, item.UpdatedAt = parseTime(created.String), parseTime(updated.String)
	return item, nil
}

const architectureTaskColumns = `id,project_id,kind,status,generation,input_json,snapshot_id,lease_owner,lease_token,lease_expires_at,cancel_requested,error_code,message,created_at,updated_at`

func (s *Store) CreateArchitectureTask(ctx context.Context, projectID, kind string, inputs ...model.ArchitectureAnalysisRequest) (model.ArchitectureTask, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "scan"
	}
	if kind != "scan" && kind != "analysis" && kind != "mcp" {
		return model.ArchitectureTask{}, errors.New("unsupported architecture task kind")
	}
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return model.ArchitectureTask{}, err
	}
	var generation int64
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0)+1 FROM architecture_tasks WHERE project_id=?`, projectID).Scan(&generation); err != nil {
		return model.ArchitectureTask{}, err
	}
	created, stamp := now()
	item := model.ArchitectureTask{ID: uuid.NewString(), ProjectID: projectID, Kind: kind, Status: "queued", Generation: generation, CreatedAt: created, UpdatedAt: created}
	inputJSON := "{}"
	if len(inputs) > 0 {
		payload, marshalErr := json.Marshal(inputs[0])
		if marshalErr != nil {
			return model.ArchitectureTask{}, marshalErr
		}
		inputJSON = string(payload)
		item.Input = &inputs[0]
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO architecture_tasks(id,project_id,kind,status,generation,input_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, item.ID, item.ProjectID, item.Kind, item.Status, item.Generation, inputJSON, stamp, stamp)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "idx_architecture_tasks_one_active") || strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return model.ArchitectureTask{}, ErrProjectTaskActive
		}
		return model.ArchitectureTask{}, err
	}
	return item, nil
}

func (s *Store) GetArchitectureTask(ctx context.Context, id string, includeLease bool) (model.ArchitectureTask, error) {
	return scanArchitectureTask(s.DB.QueryRowContext(ctx, `SELECT `+architectureTaskColumns+` FROM architecture_tasks WHERE id=?`, id), includeLease)
}

func (s *Store) ListArchitectureTasks(ctx context.Context, projectID string) ([]model.ArchitectureTask, error) {
	query := `SELECT ` + architectureTaskColumns + ` FROM architecture_tasks`
	args := []any{}
	if strings.TrimSpace(projectID) != "" {
		query += " WHERE project_id=?"
		args = append(args, projectID)
	}
	query += " ORDER BY created_at DESC LIMIT 200"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.ArchitectureTask{}
	for rows.Next() {
		item, scanErr := scanArchitectureTask(rows, false)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ClaimArchitectureTask(ctx context.Context, id, owner string, lease time.Duration) (model.ArchitectureTask, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return model.ArchitectureTask{}, errors.New("lease owner is required")
	}
	if lease <= 0 || lease > 24*time.Hour {
		lease = 5 * time.Minute
	}
	token := uuid.NewString()
	nowTime, nowStamp := now()
	expires := nowTime.Add(lease).Format(time.RFC3339Nano)
	result, err := s.DB.ExecContext(ctx, `UPDATE architecture_tasks SET status='running',lease_owner=?,lease_token=?,lease_expires_at=?,message='claimed',updated_at=? WHERE id=? AND cancel_requested=0 AND (status='queued' OR (status='running' AND lease_expires_at IS NOT NULL AND lease_expires_at<=?))`, owner, token, expires, nowStamp, id, nowStamp)
	if err != nil {
		return model.ArchitectureTask{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return model.ArchitectureTask{}, ErrTaskLeaseExpired
	}
	return s.GetArchitectureTask(ctx, id, true)
}

func (s *Store) RenewArchitectureTask(ctx context.Context, id, owner, token string, lease time.Duration) error {
	if lease <= 0 || lease > 24*time.Hour {
		lease = 5 * time.Minute
	}
	nowTime, nowStamp := now()
	expires := nowTime.Add(lease).Format(time.RFC3339Nano)
	result, err := s.DB.ExecContext(ctx, `UPDATE architecture_tasks SET lease_expires_at=?,updated_at=? WHERE id=? AND status='running' AND cancel_requested=0 AND lease_owner=? AND lease_token=? AND lease_expires_at>?`, expires, nowStamp, id, owner, token, nowStamp)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrTaskLeaseExpired
	}
	return nil
}

func (s *Store) SubmitArchitectureTask(ctx context.Context, id, owner, token, status, snapshotID, message string) (model.ArchitectureTask, error) {
	if status != "completed" && status != "failed" && status != "cancelled" {
		return model.ArchitectureTask{}, errors.New("architecture task submission must be terminal")
	}
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, `UPDATE architecture_tasks SET status=?,snapshot_id=?,message=?,lease_owner='',lease_token='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='running' AND cancel_requested=0 AND lease_owner=? AND lease_token=? AND lease_expires_at>?`, status, snapshotID, message, stamp, id, owner, token, stamp)
	if err != nil {
		return model.ArchitectureTask{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		var cancelled int
		_ = s.DB.QueryRowContext(ctx, `SELECT cancel_requested FROM architecture_tasks WHERE id=?`, id).Scan(&cancelled)
		if cancelled == 1 {
			return model.ArchitectureTask{}, ErrTaskCancelled
		}
		return model.ArchitectureTask{}, ErrTaskLeaseExpired
	}
	return s.GetArchitectureTask(ctx, id, false)
}

func (s *Store) CancelArchitectureTask(ctx context.Context, id string) (model.ArchitectureTask, error) {
	_, stamp := now()
	result, err := s.DB.ExecContext(ctx, `UPDATE architecture_tasks SET status='cancelled',cancel_requested=1,lease_owner='',lease_token='',lease_expires_at=NULL,message='Cancelled by user',updated_at=? WHERE id=? AND status IN ('queued','running','cancellation_requested')`, stamp, id)
	if err != nil {
		return model.ArchitectureTask{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return model.ArchitectureTask{}, sql.ErrNoRows
	}
	return s.GetArchitectureTask(ctx, id, false)
}

// RecoverArchitectureTasks makes tasks queued after a process restart.  A
// stale lease is never allowed to submit a result from the previous process.
func (s *Store) RecoverArchitectureTasks(ctx context.Context) error {
	_, stamp := now()
	_, err := s.DB.ExecContext(ctx, `UPDATE architecture_tasks SET status=CASE WHEN cancel_requested=1 THEN 'cancelled' ELSE 'queued' END,lease_owner='',lease_token='',lease_expires_at=NULL,message=CASE WHEN cancel_requested=1 THEN 'Cancelled during restart recovery' ELSE 'Recovered after restart' END,updated_at=? WHERE status='running'`, stamp)
	return err
}

func snapshotCoverageJSON(value model.ArchitectureCoverage) string {
	data, _ := json.Marshal(value)
	return string(data)
}

type architectureQuerier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func saveArchitectureSnapshotTx(ctx context.Context, queryer architectureQuerier, snapshot model.ArchitectureSnapshot) (model.ArchitectureSnapshot, bool, error) {
	if snapshot.ProjectID == "" || snapshot.ContentHash == "" {
		return model.ArchitectureSnapshot{}, false, errors.New("projectId and contentHash are required")
	}
	var projectHash string
	if err := queryer.QueryRowContext(ctx, `SELECT p.last_snapshot_hash FROM architecture_projects p JOIN libraries l ON l.id=p.library_id WHERE p.id=? AND p.deleted_at IS NULL AND l.deleted_at IS NULL`, snapshot.ProjectID).Scan(&projectHash); err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	if item, err := scanSnapshotMeta(queryer.QueryRowContext(ctx, `SELECT `+snapshotMetaColumns+` FROM architecture_snapshots WHERE project_id=? AND content_hash=?`, snapshot.ProjectID, snapshot.ContentHash)); err == nil {
		changed := projectHash != snapshot.ContentHash
		_, stamp := now()
		if _, err := queryer.ExecContext(ctx, `UPDATE architecture_projects SET last_snapshot_id=?,last_snapshot_hash=?,last_scan_changed=?,updated_at=? WHERE id=?`, item.ID, item.ContentHash, boolInt(changed), stamp, snapshot.ProjectID); err != nil {
			return model.ArchitectureSnapshot{}, false, err
		}
		if !changed {
			item.Status = "unchanged"
		}
		item.ProjectID = snapshot.ProjectID
		item.CreatedAt = item.CreatedAt.UTC()
		return item, changed, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.ArchitectureSnapshot{}, false, err
	}
	var generation int64
	if err := queryer.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0)+1 FROM architecture_snapshots WHERE project_id=?`, snapshot.ProjectID).Scan(&generation); err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	if snapshot.ID == "" {
		snapshot.ID = uuid.NewString()
	}
	snapshot.Generation = generation
	snapshot.Status = "complete"
	created, stamp := now()
	snapshot.CreatedAt = created
	changed := true
	gitChanged, _ := json.Marshal(snapshot.GitChangedPaths)
	if _, err := queryer.ExecContext(ctx, `INSERT INTO architecture_snapshots(id,project_id,generation,status,content_hash,git_revision,git_dirty,git_changed_paths_json,file_count,module_count,unknown_count,coverage_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, snapshot.ID, snapshot.ProjectID, snapshot.Generation, snapshot.Status, snapshot.ContentHash, snapshot.GitRevision, boolInt(snapshot.GitDirty), string(gitChanged), len(snapshot.Files), len(snapshot.Modules), snapshot.UnknownCount, snapshotCoverageJSON(snapshot.Coverage), stamp); err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	for _, file := range snapshot.Files {
		deps, _ := json.Marshal(file.DependencyFiles)
		containers, _ := json.Marshal(file.ContainerFiles)
		if _, err := queryer.ExecContext(ctx, `INSERT INTO architecture_snapshot_files(snapshot_id,path,language,size,line_start,line_end,line_count,content_hash,git_status,entry_point,dependency_files_json,container_files_json,detection) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, snapshot.ID, file.Path, file.Language, file.Size, file.LineStart, file.LineEnd, file.LineCount, file.ContentHash, file.GitStatus, boolInt(file.EntryPoint), string(deps), string(containers), file.Detection); err != nil {
			return model.ArchitectureSnapshot{}, false, err
		}
	}
	for _, module := range snapshot.Modules {
		entries, _ := json.Marshal(module.EntryPoints)
		dependencies, _ := json.Marshal(module.Dependencies)
		dependencyVersions, _ := json.Marshal(module.DependencyVersions)
		deps, _ := json.Marshal(module.DependencyFiles)
		containers, _ := json.Marshal(module.ContainerFiles)
		evidence, _ := json.Marshal(module.EvidencePaths)
		if _, err := queryer.ExecContext(ctx, `INSERT INTO architecture_snapshot_modules(snapshot_id,name,language,root_path,version,dependencies_json,dependency_versions_json,entry_points_json,dependency_files_json,container_files_json,unknown,evidence_paths_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, snapshot.ID, module.Name, module.Language, module.RootPath, module.Version, string(dependencies), string(dependencyVersions), string(entries), string(deps), string(containers), boolInt(module.Unknown), string(evidence)); err != nil {
			return model.ArchitectureSnapshot{}, false, err
		}
	}
	if _, err := queryer.ExecContext(ctx, `UPDATE architecture_projects SET last_snapshot_id=?,last_snapshot_hash=?,last_scan_changed=?,updated_at=? WHERE id=? AND deleted_at IS NULL`, snapshot.ID, snapshot.ContentHash, boolInt(changed), stamp, snapshot.ProjectID); err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	return snapshot, changed, nil
}

func (s *Store) SaveArchitectureSnapshot(ctx context.Context, snapshot model.ArchitectureSnapshot) (model.ArchitectureSnapshot, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	defer tx.Rollback()
	item, changed, err := saveArchitectureSnapshotTx(ctx, tx, snapshot)
	if err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	snapshotStatus := item.Status
	item, err = s.GetArchitectureSnapshot(ctx, snapshot.ProjectID, item.ID)
	if err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	item.Status = snapshotStatus
	return item, changed, nil
}

// SaveArchitectureSnapshotForTask commits the snapshot and terminal task
// result under one SQLite transaction.  A stale process cannot write a newer
// snapshot after its lease expires.
func (s *Store) SaveArchitectureSnapshotForTask(ctx context.Context, taskID, owner, token string, snapshot model.ArchitectureSnapshot) (model.ArchitectureSnapshot, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	defer tx.Rollback()
	var projectID, status, leaseOwner, leaseToken, leaseExpiry string
	var cancelRequested int
	if err := tx.QueryRowContext(ctx, `SELECT project_id,status,lease_owner,lease_token,COALESCE(lease_expires_at,''),cancel_requested FROM architecture_tasks WHERE id=?`, taskID).Scan(&projectID, &status, &leaseOwner, &leaseToken, &leaseExpiry, &cancelRequested); err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	_, nowStamp := now()
	if status != "running" || cancelRequested != 0 || projectID != snapshot.ProjectID || leaseOwner != owner || leaseToken != token || leaseExpiry == "" || leaseExpiry <= nowStamp {
		if cancelRequested != 0 {
			return model.ArchitectureSnapshot{}, false, ErrTaskCancelled
		}
		return model.ArchitectureSnapshot{}, false, ErrTaskLeaseExpired
	}
	item, changed, err := saveArchitectureSnapshotTx(ctx, tx, snapshot)
	if err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE architecture_tasks SET status='completed',snapshot_id=?,message='Snapshot saved',lease_owner='',lease_token='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='running' AND cancel_requested=0 AND lease_owner=? AND lease_token=? AND lease_expires_at>?`, item.ID, nowStamp, taskID, owner, token, nowStamp)
	if err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return model.ArchitectureSnapshot{}, false, ErrTaskLeaseExpired
	}
	if err := tx.Commit(); err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	snapshotStatus := item.Status
	item, err = s.GetArchitectureSnapshot(ctx, snapshot.ProjectID, item.ID)
	if err != nil {
		return model.ArchitectureSnapshot{}, false, err
	}
	item.Status = snapshotStatus
	return item, changed, nil
}

func scanSnapshotMeta(row rowScanner) (model.ArchitectureSnapshot, error) {
	var item model.ArchitectureSnapshot
	var gitDirty int
	var changed, coverage, created string
	if err := row.Scan(&item.ID, &item.ProjectID, &item.Generation, &item.Status, &item.ContentHash, &item.GitRevision, &gitDirty, &changed, &item.FileCount, &item.ModuleCount, &item.UnknownCount, &coverage, &created); err != nil {
		return item, err
	}
	item.GitDirty = gitDirty == 1
	_ = json.Unmarshal([]byte(changed), &item.GitChangedPaths)
	if item.GitChangedPaths == nil {
		item.GitChangedPaths = []string{}
	}
	_ = json.Unmarshal([]byte(coverage), &item.Coverage)
	if item.Coverage.ByLanguage == nil {
		item.Coverage.ByLanguage = map[string]int{}
	}
	if item.Coverage.IgnoredReasons == nil {
		item.Coverage.IgnoredReasons = map[string]int{}
	}
	item.CreatedAt = parseTime(created)
	return item, nil
}

const snapshotMetaColumns = `id,project_id,generation,status,content_hash,git_revision,git_dirty,git_changed_paths_json,file_count,module_count,unknown_count,coverage_json,created_at`

func (s *Store) GetArchitectureSnapshotByHash(ctx context.Context, projectID, hash string) (model.ArchitectureSnapshot, error) {
	item, err := scanSnapshotMeta(s.DB.QueryRowContext(ctx, `SELECT `+snapshotMetaColumns+` FROM architecture_snapshots WHERE project_id=? AND content_hash=?`, projectID, hash))
	if err != nil {
		return item, err
	}
	return s.loadSnapshotChildren(ctx, item)
}

func (s *Store) GetLatestArchitectureSnapshot(ctx context.Context, projectID string) (model.ArchitectureSnapshot, error) {
	item, err := scanSnapshotMeta(s.DB.QueryRowContext(ctx, `SELECT `+snapshotMetaColumns+` FROM architecture_snapshots WHERE project_id=? ORDER BY generation DESC LIMIT 1`, projectID))
	if err != nil {
		return item, err
	}
	return s.loadSnapshotChildren(ctx, item)
}

func (s *Store) GetArchitectureSnapshot(ctx context.Context, projectID, id string) (model.ArchitectureSnapshot, error) {
	item, err := scanSnapshotMeta(s.DB.QueryRowContext(ctx, `SELECT `+snapshotMetaColumns+` FROM architecture_snapshots WHERE project_id=? AND id=?`, projectID, id))
	if err != nil {
		return item, err
	}
	return s.loadSnapshotChildren(ctx, item)
}

func (s *Store) ListArchitectureSnapshots(ctx context.Context, projectID string) ([]model.ArchitectureSnapshot, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+snapshotMetaColumns+` FROM architecture_snapshots WHERE project_id=? ORDER BY generation DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.ArchitectureSnapshot{}
	for rows.Next() {
		item, scanErr := scanSnapshotMeta(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) loadSnapshotChildren(ctx context.Context, item model.ArchitectureSnapshot) (model.ArchitectureSnapshot, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path,language,size,line_start,line_end,line_count,content_hash,git_status,entry_point,dependency_files_json,container_files_json,detection FROM architecture_snapshot_files WHERE snapshot_id=? ORDER BY path`, item.ID)
	if err != nil {
		return item, err
	}
	for rows.Next() {
		var file model.ArchitectureFile
		var entry int
		var deps, containers string
		if err := rows.Scan(&file.Path, &file.Language, &file.Size, &file.LineStart, &file.LineEnd, &file.LineCount, &file.ContentHash, &file.GitStatus, &entry, &deps, &containers, &file.Detection); err != nil {
			rows.Close()
			return item, err
		}
		file.EntryPoint = entry == 1
		_ = json.Unmarshal([]byte(deps), &file.DependencyFiles)
		_ = json.Unmarshal([]byte(containers), &file.ContainerFiles)
		item.Files = append(item.Files, file)
	}
	if err := rows.Close(); err != nil {
		return item, err
	}
	moduleRows, err := s.DB.QueryContext(ctx, `SELECT name,language,root_path,version,dependencies_json,dependency_versions_json,entry_points_json,dependency_files_json,container_files_json,unknown,evidence_paths_json FROM architecture_snapshot_modules WHERE snapshot_id=? ORDER BY root_path,name`, item.ID)
	if err != nil {
		return item, err
	}
	for moduleRows.Next() {
		var module model.ArchitectureModule
		var entries, dependencies, dependencyVersions, deps, containers, evidence string
		var unknown int
		if err := moduleRows.Scan(&module.Name, &module.Language, &module.RootPath, &module.Version, &dependencies, &dependencyVersions, &entries, &deps, &containers, &unknown, &evidence); err != nil {
			moduleRows.Close()
			return item, err
		}
		_ = json.Unmarshal([]byte(entries), &module.EntryPoints)
		_ = json.Unmarshal([]byte(dependencies), &module.Dependencies)
		_ = json.Unmarshal([]byte(dependencyVersions), &module.DependencyVersions)
		_ = json.Unmarshal([]byte(deps), &module.DependencyFiles)
		_ = json.Unmarshal([]byte(containers), &module.ContainerFiles)
		_ = json.Unmarshal([]byte(evidence), &module.EvidencePaths)
		item.Modules = append(item.Modules, module)
		item.Modules[len(item.Modules)-1].Unknown = unknown == 1
	}
	if err := moduleRows.Close(); err != nil {
		return item, err
	}
	return item, moduleRows.Err()
}

func (s *Store) SaveArchitectureAnalysis(ctx context.Context, analysis model.ArchitectureAnalysis) error {
	if analysis.SnapshotID == "" || analysis.ProjectID == "" {
		return errors.New("projectId and snapshotId are required")
	}
	if err := s.ValidateArchitectureAnalysis(ctx, analysis); err != nil {
		return err
	}
	payload, err := json.Marshal(analysis)
	if err != nil {
		return err
	}
	_, stamp := now()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO architecture_analyses(snapshot_id,payload_json,created_at) VALUES(?,?,?) ON CONFLICT(snapshot_id) DO UPDATE SET payload_json=excluded.payload_json,created_at=excluded.created_at`, analysis.SnapshotID, string(payload), stamp)
	return err
}

func saveArchitectureAnalysisTx(ctx context.Context, queryer architectureQuerier, analysis model.ArchitectureAnalysis, created string, owner *Store) error {
	if err := owner.validateArchitectureAnalysis(ctx, queryer, analysis); err != nil {
		return err
	}
	payload, err := json.Marshal(analysis)
	if err != nil {
		return err
	}
	_, err = queryer.ExecContext(ctx, `INSERT INTO architecture_analyses(snapshot_id,payload_json,created_at) VALUES(?,?,?) ON CONFLICT(snapshot_id) DO UPDATE SET payload_json=excluded.payload_json,created_at=excluded.created_at`, analysis.SnapshotID, string(payload), created)
	return err
}

type architectureFileTarget struct {
	lineStart int
	lineEnd   int
	lineCount int
	hash      string
}

func (s *Store) architectureSnapshotFiles(ctx context.Context, queryer architectureQuerier, snapshotID string) (map[string]architectureFileTarget, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT path,line_start,line_end,line_count,content_hash FROM architecture_snapshot_files WHERE snapshot_id=?`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := map[string]architectureFileTarget{}
	for rows.Next() {
		var path, hash string
		var start, end, count int
		if err := rows.Scan(&path, &start, &end, &count, &hash); err != nil {
			return nil, err
		}
		files[path] = architectureFileTarget{lineStart: start, lineEnd: end, lineCount: count, hash: hash}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return files, nil
}

func validAnalysisID(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

// ValidateArchitectureAnalysis keeps facts and inferences tied to immutable
// snapshot evidence. Later stages may add more fields, but they cannot invent
// a path, line range, or content hash outside the captured snapshot.
func (s *Store) ValidateArchitectureAnalysis(ctx context.Context, analysis model.ArchitectureAnalysis) error {
	return s.validateArchitectureAnalysis(ctx, s.DB, analysis)
}

func (s *Store) validateArchitectureAnalysis(ctx context.Context, queryer architectureQuerier, analysis model.ArchitectureAnalysis) error {
	if analysis.SchemaVersion != "kah.architecture.analysis.v1" {
		return errors.New("schemaVersion must be kah.architecture.analysis.v1")
	}
	var snapshotProject string
	if err := queryer.QueryRowContext(ctx, `SELECT project_id FROM architecture_snapshots WHERE id=?`, analysis.SnapshotID).Scan(&snapshotProject); err != nil {
		return err
	}
	if snapshotProject != analysis.ProjectID {
		return sql.ErrNoRows
	}
	files, err := s.architectureSnapshotFiles(ctx, queryer, analysis.SnapshotID)
	if err != nil {
		return err
	}
	evidence := map[string]bool{}
	for _, item := range analysis.Evidence {
		if !validAnalysisID(item.ID) || evidence[item.ID] {
			return errors.New("analysis evidence IDs must be unique and non-empty")
		}
		if item.Path == "" || strings.TrimSpace(item.Path) != item.Path {
			return fmt.Errorf("analysis evidence %q has an invalid path", item.ID)
		}
		target, ok := files[item.Path]
		if !ok {
			return fmt.Errorf("analysis evidence path %q is not in the snapshot", item.Path)
		}
		if item.LineStart < 1 || item.LineEnd < item.LineStart || item.LineEnd > target.lineCount || item.ContentHash != target.hash || strings.TrimSpace(item.Kind) == "" {
			return fmt.Errorf("analysis evidence %q does not match snapshot line/hash", item.ID)
		}
		evidence[item.ID] = true
	}
	entityIDs := map[string]string{}
	for _, fact := range analysis.Facts {
		if !validAnalysisID(fact.ID) || strings.TrimSpace(fact.Subject) == "" || strings.TrimSpace(fact.Predicate) == "" || strings.TrimSpace(fact.Object) == "" || len(fact.Evidence) == 0 {
			return errors.New("analysis facts require id, subject, predicate, object, and evidence")
		}
		if previous, exists := entityIDs[fact.ID]; exists {
			return fmt.Errorf("analysis ID %q is duplicated by %s and fact", fact.ID, previous)
		}
		entityIDs[fact.ID] = "fact"
		for _, id := range fact.Evidence {
			if !evidence[id] {
				return fmt.Errorf("fact %q references unknown evidence %q", fact.ID, id)
			}
		}
	}
	for _, inference := range analysis.Inferences {
		if !validAnalysisID(inference.ID) || strings.TrimSpace(inference.Statement) == "" || len(inference.Evidence) == 0 || inference.Confidence < 0 || inference.Confidence > 1 {
			return errors.New("analysis inferences require id, statement, evidence, and confidence between 0 and 1")
		}
		if previous, exists := entityIDs[inference.ID]; exists {
			return fmt.Errorf("analysis ID %q is duplicated by %s and inference", inference.ID, previous)
		}
		entityIDs[inference.ID] = "inference"
		for _, id := range inference.Evidence {
			if !evidence[id] {
				return fmt.Errorf("inference %q references unknown evidence %q", inference.ID, id)
			}
		}
	}

	groups := []struct {
		name  string
		paths []string
	}{
		{name: "added", paths: analysis.Incremental.Added},
		{name: "changed", paths: analysis.Incremental.Changed},
		{name: "deleted", paths: analysis.Incremental.Deleted},
		{name: "unchanged", paths: analysis.Incremental.Unchanged},
	}
	hasIncremental := false
	for _, group := range groups {
		if len(group.paths) > 0 {
			hasIncremental = true
			break
		}
	}
	if analysis.Incremental.BaseSnapshotID == "" {
		if hasIncremental {
			return errors.New("incremental changes require baseSnapshotId")
		}
		return nil
	}
	var baseProject string
	if err := queryer.QueryRowContext(ctx, `SELECT project_id FROM architecture_snapshots WHERE id=?`, analysis.Incremental.BaseSnapshotID).Scan(&baseProject); err != nil {
		return err
	}
	if baseProject != analysis.ProjectID {
		return errors.New("baseSnapshotId must belong to the analysis project")
	}
	baseFiles, err := s.architectureSnapshotFiles(ctx, queryer, analysis.Incremental.BaseSnapshotID)
	if err != nil {
		return err
	}
	seenPaths := map[string]string{}
	for _, group := range groups {
		for _, path := range group.paths {
			if path == "" || strings.TrimSpace(path) != path {
				return fmt.Errorf("incremental %s contains an invalid path", group.name)
			}
			if previous, exists := seenPaths[path]; exists {
				return fmt.Errorf("incremental path %q is listed as both %s and %s", path, previous, group.name)
			}
			seenPaths[path] = group.name
			_, currentOK := files[path]
			_, baseOK := baseFiles[path]
			switch group.name {
			case "added":
				if !currentOK || baseOK {
					return fmt.Errorf("incremental added path %q does not exist only in the current snapshot", path)
				}
			case "changed":
				if !currentOK || !baseOK || files[path].hash == baseFiles[path].hash {
					return fmt.Errorf("incremental changed path %q does not have different current and base hashes", path)
				}
			case "deleted":
				if currentOK || !baseOK {
					return fmt.Errorf("incremental deleted path %q does not exist only in the base snapshot", path)
				}
			case "unchanged":
				if !currentOK || !baseOK || files[path].hash != baseFiles[path].hash {
					return fmt.Errorf("incremental unchanged path %q does not have the same current and base hash", path)
				}
			}
		}
	}
	return nil
}

func (s *Store) GetArchitectureAnalysis(ctx context.Context, projectID, snapshotID string) (model.ArchitectureAnalysis, error) {
	var payload string
	if err := s.DB.QueryRowContext(ctx, `SELECT aa.payload_json FROM architecture_analyses aa JOIN architecture_snapshots s ON s.id=aa.snapshot_id WHERE aa.snapshot_id=? AND s.project_id=?`, snapshotID, projectID).Scan(&payload); err != nil {
		return model.ArchitectureAnalysis{}, err
	}
	var result model.ArchitectureAnalysis
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return result, fmt.Errorf("decode architecture analysis: %w", err)
	}
	return result, nil
}

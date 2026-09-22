package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/google/uuid"
)

type ArchitectureArtifactInput struct {
	Kind          string
	FileName      string
	ContentType   string
	Renderer      string
	RendererError string
	Data          []byte
}

func artifactKindAllowed(kind string) bool {
	switch kind {
	case "document", "diagram_json", "html", "svg", "png":
		return true
	default:
		return false
	}
}

func (s *Store) SaveArchitectureArtifacts(ctx context.Context, projectID, snapshotID string, inputs []ArchitectureArtifactInput) ([]model.ArchitectureArtifact, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(snapshotID) == "" {
		return nil, errors.New("projectId and snapshotId are required")
	}
	var snapshotProject string
	if err := s.DB.QueryRowContext(ctx, `SELECT project_id FROM architecture_snapshots WHERE id=?`, snapshotID).Scan(&snapshotProject); err != nil {
		return nil, err
	}
	if snapshotProject != projectID {
		return nil, sql.ErrNoRows
	}
	if len(inputs) == 0 {
		return nil, errors.New("at least one architecture artifact is required")
	}
	root := filepath.Join(s.DataRoot, "architecture", projectID, snapshotID)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	items := make([]model.ArchitectureArtifact, 0, len(inputs))
	for _, input := range inputs {
		if !artifactKindAllowed(input.Kind) || strings.TrimSpace(input.FileName) == "" || strings.ContainsAny(input.FileName, `/\\`) {
			return nil, fmt.Errorf("invalid architecture artifact %q", input.Kind)
		}
		if input.ContentType == "" {
			return nil, fmt.Errorf("content type is required for %s", input.Kind)
		}
		sum := sha256.Sum256(input.Data)
		hash := hex.EncodeToString(sum[:])
		tmp, err := os.CreateTemp(root, ".artifact-*")
		if err != nil {
			return nil, err
		}
		tmpPath := tmp.Name()
		if _, err = tmp.Write(input.Data); err == nil {
			err = tmp.Close()
		} else {
			_ = tmp.Close()
		}
		if err != nil {
			_ = os.Remove(tmpPath)
			return nil, err
		}
		destination := filepath.Join(root, input.FileName)
		if err = os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(tmpPath)
			return nil, err
		}
		if err = os.Rename(tmpPath, destination); err != nil {
			_ = os.Remove(tmpPath)
			return nil, err
		}
		stamp := time.Now().UTC()
		item := model.ArchitectureArtifact{ID: uuid.NewString(), ProjectID: projectID, SnapshotID: snapshotID, Kind: input.Kind, FileName: input.FileName, ContentType: input.ContentType, RelativePath: filepath.ToSlash(filepath.Join("architecture", projectID, snapshotID, input.FileName)), ContentHash: hash, Size: int64(len(input.Data)), CreatedAt: stamp}
		_, err = s.DB.ExecContext(ctx, `INSERT INTO architecture_artifacts(id,project_id,snapshot_id,kind,file_name,content_type,renderer,renderer_error,relative_path,content_hash,size,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(snapshot_id,kind) DO UPDATE SET id=excluded.id,file_name=excluded.file_name,content_type=excluded.content_type,renderer=excluded.renderer,renderer_error=excluded.renderer_error,relative_path=excluded.relative_path,content_hash=excluded.content_hash,size=excluded.size,created_at=excluded.created_at`, item.ID, item.ProjectID, item.SnapshotID, item.Kind, item.FileName, item.ContentType, input.Renderer, input.RendererError, item.RelativePath, item.ContentHash, item.Size, stamp.Format(time.RFC3339Nano))
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Kind < items[j].Kind })
	return items, nil
}

func (s *Store) ListArchitectureArtifacts(ctx context.Context, projectID, snapshotID string) ([]model.ArchitectureArtifact, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,project_id,snapshot_id,kind,file_name,content_type,renderer,renderer_error,relative_path,content_hash,size,created_at FROM architecture_artifacts WHERE project_id=? AND snapshot_id=? ORDER BY kind`, projectID, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.ArchitectureArtifact{}
	for rows.Next() {
		var item model.ArchitectureArtifact
		var created string
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.SnapshotID, &item.Kind, &item.FileName, &item.ContentType, &item.Renderer, &item.RendererError, &item.RelativePath, &item.ContentHash, &item.Size, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetArchitectureArtifact(ctx context.Context, projectID, snapshotID, kind string) (model.ArchitectureArtifact, error) {
	var item model.ArchitectureArtifact
	var created string
	err := s.DB.QueryRowContext(ctx, `SELECT id,project_id,snapshot_id,kind,file_name,content_type,renderer,renderer_error,relative_path,content_hash,size,created_at FROM architecture_artifacts WHERE project_id=? AND snapshot_id=? AND kind=?`, projectID, snapshotID, kind).Scan(&item.ID, &item.ProjectID, &item.SnapshotID, &item.Kind, &item.FileName, &item.ContentType, &item.Renderer, &item.RendererError, &item.RelativePath, &item.ContentHash, &item.Size, &created)
	item.CreatedAt = parseTime(created)
	return item, err
}

func (s *Store) ReadArchitectureArtifact(item model.ArchitectureArtifact) ([]byte, error) {
	if item.RelativePath == "" {
		return nil, errors.New("artifact path is empty")
	}
	root, err := filepath.Abs(s.DataRoot)
	if err != nil {
		return nil, err
	}
	target, err := filepath.Abs(filepath.Join(s.DataRoot, filepath.FromSlash(item.RelativePath)))
	if err != nil {
		return nil, err
	}
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return nil, errors.New("artifact path escapes data root")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != item.ContentHash {
		return nil, errors.New("architecture artifact hash mismatch")
	}
	return data, nil
}

func (s *Store) SetArchitectureAnalysisState(ctx context.Context, projectID, status, message, snapshotID string) error {
	if status == "" {
		return errors.New("analysis status is required")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE architecture_projects SET analysis_status=?,analysis_error=?,current_analysis_snapshot_id=?,updated_at=? WHERE id=? AND deleted_at IS NULL`, status, message, snapshotID, time.Now().UTC().Format(time.RFC3339Nano), projectID)
	return err
}

// CommitArchitectureAnalysisTask publishes a generated analysis and closes
// its API-owned lease in one transaction.  The lease/cancel predicates are
// part of the same transaction as the current pointer update, so a user
// cancellation or process shutdown cannot race the final write and publish a
// result after the task has stopped.
func (s *Store) CommitArchitectureAnalysisTask(ctx context.Context, taskID, owner, token, snapshotID string) error {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(owner) == "" || strings.TrimSpace(token) == "" || strings.TrimSpace(snapshotID) == "" {
		return ErrTaskLeaseExpired
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID, status, leaseOwner, leaseToken, leaseExpiry string
	var cancelRequested int
	if err := tx.QueryRowContext(ctx, `SELECT project_id,status,lease_owner,lease_token,COALESCE(lease_expires_at,''),cancel_requested FROM architecture_tasks WHERE id=?`, taskID).Scan(&projectID, &status, &leaseOwner, &leaseToken, &leaseExpiry, &cancelRequested); err != nil {
		return err
	}
	_, stamp := now()
	if status != "running" || cancelRequested != 0 || leaseOwner != owner || leaseToken != token || leaseExpiry == "" || leaseExpiry <= stamp {
		if cancelRequested != 0 || status == "cancelled" {
			return ErrTaskCancelled
		}
		return ErrTaskLeaseExpired
	}
	var analysisExists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM architecture_analyses WHERE snapshot_id=?`, snapshotID).Scan(&analysisExists); err != nil {
		return err
	}
	if analysisExists != 1 {
		return errors.New("architecture analysis does not exist for snapshot")
	}
	result, err := tx.ExecContext(ctx, `UPDATE architecture_projects SET analysis_status='current',analysis_error='',current_analysis_snapshot_id=?,updated_at=? WHERE id=? AND deleted_at IS NULL`, snapshotID, stamp, projectID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return sql.ErrNoRows
	}
	result, err = tx.ExecContext(ctx, `UPDATE architecture_tasks SET status='completed',snapshot_id=?,message='Architecture generated',lease_owner='',lease_token='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='running' AND cancel_requested=0 AND lease_owner=? AND lease_token=? AND lease_expires_at>?`, snapshotID, stamp, taskID, owner, token, stamp)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrTaskLeaseExpired
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) RegisterArchitectureAgent(ctx context.Context, input model.ArchitectureAgentRegistration) (model.ArchitectureAgent, error) {
	credential, err := s.RegisterArchitectureAgentCredential(ctx, input)
	return credential.Agent, err
}

func (s *Store) RegisterArchitectureAgentCredential(ctx context.Context, input model.ArchitectureAgentRegistration) (model.ArchitectureAgentCredential, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 200 {
		return model.ArchitectureAgentCredential{}, errors.New("agent name is required")
	}
	if len(input.ProjectIDs) == 0 {
		return model.ArchitectureAgentCredential{}, errors.New("agent must be scoped to at least one project")
	}
	id := uuid.NewString()
	secret := "kah_arch_" + uuid.NewString() + uuid.NewString()
	sum := sha256.Sum256([]byte(secret))
	stamp := time.Now().UTC()
	caps := uniqueStrings(input.Capabilities)
	projects := uniqueStrings(input.ProjectIDs)
	capsJSON, _ := json.Marshal(caps)
	projectsJSON, _ := json.Marshal(projects)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO architecture_agents(id,name,capabilities_json,project_ids_json,status,token_hash,last_heartbeat,created_at) VALUES(?,?,?,?,?,?,?,?)`, id, name, string(capsJSON), string(projectsJSON), "online", hex.EncodeToString(sum[:]), stamp.Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano))
	if err != nil {
		return model.ArchitectureAgentCredential{}, err
	}
	return model.ArchitectureAgentCredential{Agent: model.ArchitectureAgent{ID: id, Name: name, Capabilities: caps, ProjectIDs: projects, Status: "online", LastHeartbeat: stamp, CreatedAt: stamp}, Secret: secret}, nil
}

func (s *Store) AuthenticateArchitectureAgent(ctx context.Context, secret string) (model.ArchitectureAgent, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return model.ArchitectureAgent{}, sql.ErrNoRows
	}
	sum := sha256.Sum256([]byte(secret))
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM architecture_agents WHERE token_hash=? AND status<>'revoked'`, hex.EncodeToString(sum[:])).Scan(&id); err != nil {
		return model.ArchitectureAgent{}, err
	}
	return s.GetArchitectureAgent(ctx, id)
}

func (s *Store) GetArchitectureAgent(ctx context.Context, id string) (model.ArchitectureAgent, error) {
	var item model.ArchitectureAgent
	var caps, projects, heartbeat, created string
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,capabilities_json,project_ids_json,status,last_heartbeat,created_at FROM architecture_agents WHERE id=?`, id).Scan(&item.ID, &item.Name, &caps, &projects, &item.Status, &heartbeat, &created)
	if err != nil {
		return item, err
	}
	_ = json.Unmarshal([]byte(caps), &item.Capabilities)
	_ = json.Unmarshal([]byte(projects), &item.ProjectIDs)
	item.LastHeartbeat, item.CreatedAt = parseTime(heartbeat), parseTime(created)
	return item, nil
}

func (s *Store) ListArchitectureAgents(ctx context.Context) ([]model.ArchitectureAgent, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,capabilities_json,project_ids_json,status,last_heartbeat,created_at FROM architecture_agents ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.ArchitectureAgent{}
	for rows.Next() {
		var item model.ArchitectureAgent
		var caps, projects, heartbeat, created string
		if err := rows.Scan(&item.ID, &item.Name, &caps, &projects, &item.Status, &heartbeat, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(caps), &item.Capabilities)
		_ = json.Unmarshal([]byte(projects), &item.ProjectIDs)
		item.LastHeartbeat, item.CreatedAt = parseTime(heartbeat), parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) HeartbeatArchitectureAgent(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE architecture_agents SET status='online',last_heartbeat=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ArchitectureAgentMayUseProject(agent model.ArchitectureAgent, projectID string) bool {
	return len(agent.ProjectIDs) > 0 && containsString(agent.ProjectIDs, projectID)
}

// ArchitectureAgentCanAnalyze keeps project scope and task capability as two
// separate checks. A credential that is scoped to a project but was registered
// for another purpose must not be able to claim architecture source material.
func (s *Store) ArchitectureAgentCanAnalyze(agent model.ArchitectureAgent, projectID string) bool {
	if agent.Status != "online" || agent.LastHeartbeat.IsZero() || time.Since(agent.LastHeartbeat) > 10*time.Minute || !s.ArchitectureAgentMayUseProject(agent, projectID) {
		return false
	}
	for _, capability := range agent.Capabilities {
		switch strings.ToLower(strings.TrimSpace(capability)) {
		case "architecture.analysis", "architecture", "*":
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// ClaimNextArchitectureTask performs a compare-and-set claim through the
// existing lease function. At most one agent can win each queued task.
func (s *Store) ClaimNextArchitectureTask(ctx context.Context, agent model.ArchitectureAgent, lease time.Duration) (model.ArchitectureTask, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,project_id FROM architecture_tasks WHERE kind IN ('analysis','mcp') AND status='queued' AND cancel_requested=0 ORDER BY created_at,id LIMIT 100`)
	if err != nil {
		return model.ArchitectureTask{}, err
	}
	type candidate struct{ id, projectID string }
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.projectID); err != nil {
			rows.Close()
			return model.ArchitectureTask{}, err
		}
		if s.ArchitectureAgentCanAnalyze(agent, item.projectID) {
			candidates = append(candidates, item)
		}
	}
	if err := rows.Close(); err != nil {
		return model.ArchitectureTask{}, err
	}
	for _, item := range candidates {
		claimed, claimErr := s.ClaimArchitectureTask(ctx, item.id, "agent:"+agent.ID, lease)
		if claimErr == nil {
			return claimed, nil
		}
	}
	return model.ArchitectureTask{}, sql.ErrNoRows
}

func (s *Store) SubmitArchitectureAgentFailure(ctx context.Context, taskID, agentID, token, message string) (model.ArchitectureTask, error) {
	return s.SubmitArchitectureTask(ctx, taskID, "agent:"+agentID, token, "failed", "", message)
}

func (s *Store) SubmitArchitectureAgentResult(ctx context.Context, taskID, agentID, token string, analysis model.ArchitectureAnalysis, artifacts []ArchitectureArtifactInput, generations ...int64) (model.ArchitectureTask, error) {
	if strings.TrimSpace(agentID) == "" || strings.TrimSpace(token) == "" {
		return model.ArchitectureTask{}, ErrTaskLeaseExpired
	}
	if analysis.ProjectID == "" || analysis.SnapshotID == "" {
		return model.ArchitectureTask{}, errors.New("analysis projectId and snapshotId are required")
	}
	if len(generations) > 0 && generations[0] == 0 {
		return model.ArchitectureTask{}, errors.New("analysis task generation is required")
	}
	prepared, cleanup, err := s.prepareArchitectureAgentArtifacts(analysis.ProjectID, analysis.SnapshotID, artifacts)
	if err != nil {
		return model.ArchitectureTask{}, err
	}
	committed := false
	defer func() { cleanup(!committed) }()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.ArchitectureTask{}, err
	}
	defer tx.Rollback()
	var projectID, kind, status, leaseOwner, leaseToken, leaseExpiry, inputJSON string
	var cancelRequested, generation int64
	if err := tx.QueryRowContext(ctx, `SELECT project_id,kind,status,lease_owner,lease_token,COALESCE(lease_expires_at,''),cancel_requested,generation,input_json FROM architecture_tasks WHERE id=?`, taskID).Scan(&projectID, &kind, &status, &leaseOwner, &leaseToken, &leaseExpiry, &cancelRequested, &generation, &inputJSON); err != nil {
		return model.ArchitectureTask{}, err
	}
	_, nowStamp := now()
	if status != "running" || cancelRequested != 0 || projectID != analysis.ProjectID || leaseOwner != "agent:"+agentID || leaseToken != token || leaseExpiry == "" || leaseExpiry <= nowStamp || (len(generations) > 0 && generation != generations[0]) {
		if cancelRequested != 0 {
			return model.ArchitectureTask{}, ErrTaskCancelled
		}
		return model.ArchitectureTask{}, ErrTaskLeaseExpired
	}
	var snapshotProject string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM architecture_snapshots WHERE id=?`, analysis.SnapshotID).Scan(&snapshotProject); err != nil {
		return model.ArchitectureTask{}, err
	}
	if snapshotProject != analysis.ProjectID {
		return model.ArchitectureTask{}, sql.ErrNoRows
	}
	if strings.TrimSpace(inputJSON) != "" && strings.TrimSpace(inputJSON) != "{}" {
		var input model.ArchitectureAnalysisRequest
		if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
			return model.ArchitectureTask{}, errors.New("architecture task input is invalid")
		}
		if input.Snapshot.ID != "" && input.Snapshot.ID != analysis.SnapshotID {
			return model.ArchitectureTask{}, errors.New("architecture result snapshot does not match task input")
		}
	}
	if err := saveArchitectureAnalysisTx(ctx, tx, analysis, nowStamp, s); err != nil {
		return model.ArchitectureTask{}, err
	}
	for _, item := range prepared {
		_, err := tx.ExecContext(ctx, `INSERT INTO architecture_artifacts(id,project_id,snapshot_id,kind,file_name,content_type,renderer,renderer_error,relative_path,content_hash,size,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(snapshot_id,kind) DO UPDATE SET id=excluded.id,file_name=excluded.file_name,content_type=excluded.content_type,renderer=excluded.renderer,renderer_error=excluded.renderer_error,relative_path=excluded.relative_path,content_hash=excluded.content_hash,size=excluded.size,created_at=excluded.created_at`, item.record.ID, item.record.ProjectID, item.record.SnapshotID, item.record.Kind, item.record.FileName, item.record.ContentType, item.record.Renderer, item.record.RendererError, item.record.RelativePath, item.record.ContentHash, item.record.Size, nowStamp)
		if err != nil {
			return model.ArchitectureTask{}, err
		}
	}
	if kind != "mcp" {
		if _, err := tx.ExecContext(ctx, `UPDATE architecture_projects SET analysis_status='current',analysis_error='',current_analysis_snapshot_id=?,updated_at=? WHERE id=? AND deleted_at IS NULL`, analysis.SnapshotID, nowStamp, analysis.ProjectID); err != nil {
			return model.ArchitectureTask{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE architecture_tasks SET status='completed',snapshot_id=?,message='Analysis saved',lease_owner='',lease_token='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='running' AND cancel_requested=0 AND lease_owner=? AND lease_token=? AND lease_expires_at>? AND generation=?`, analysis.SnapshotID, nowStamp, taskID, "agent:"+agentID, token, nowStamp, generation)
	if err != nil {
		return model.ArchitectureTask{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return model.ArchitectureTask{}, ErrTaskLeaseExpired
	}
	// Publish unique files before the transaction is committed. A rollback
	// removes them; no existing artifact path is ever overwritten.
	for _, item := range prepared {
		if err := os.Rename(item.pendingPath, item.destinationPath); err != nil {
			return model.ArchitectureTask{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.ArchitectureTask{}, err
	}
	committed = true
	return s.GetArchitectureTask(ctx, taskID, false)
}

type preparedArchitectureArtifact struct {
	record          model.ArchitectureArtifact
	pendingPath     string
	destinationPath string
}

func (s *Store) prepareArchitectureAgentArtifacts(projectID, snapshotID string, inputs []ArchitectureArtifactInput) ([]preparedArchitectureArtifact, func(bool), error) {
	if len(inputs) == 0 {
		return nil, func(bool) {}, nil
	}
	root := filepath.Join(s.DataRoot, "architecture", projectID, snapshotID)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, func(bool) {}, err
	}
	pendingRoot := filepath.Join(root, ".pending-"+uuid.NewString())
	if err := os.MkdirAll(pendingRoot, 0o700); err != nil {
		return nil, func(bool) {}, err
	}
	items := make([]preparedArchitectureArtifact, 0, len(inputs))
	seenKinds := map[string]bool{}
	cleanup := func(removeDestination bool) {
		for _, item := range items {
			_ = os.Remove(item.pendingPath)
			if removeDestination {
				_ = os.Remove(item.destinationPath)
			}
		}
		_ = os.RemoveAll(pendingRoot)
	}
	for _, input := range inputs {
		if !artifactKindAllowed(input.Kind) || strings.TrimSpace(input.FileName) == "" || strings.ContainsAny(input.FileName, `/\\`) || seenKinds[input.Kind] {
			cleanup(false)
			return nil, func(bool) {}, fmt.Errorf("invalid architecture artifact %q", input.Kind)
		}
		if input.ContentType == "" {
			cleanup(false)
			return nil, func(bool) {}, fmt.Errorf("content type is required for %s", input.Kind)
		}
		seenKinds[input.Kind] = true
		sum := sha256.Sum256(input.Data)
		digest := hex.EncodeToString(sum[:])
		name := uuid.NewString() + "-" + input.FileName
		pending := filepath.Join(pendingRoot, name)
		destination := filepath.Join(root, name)
		if err := os.WriteFile(pending, input.Data, 0o640); err != nil {
			cleanup(false)
			return nil, func(bool) {}, err
		}
		items = append(items, preparedArchitectureArtifact{record: model.ArchitectureArtifact{ID: uuid.NewString(), ProjectID: projectID, SnapshotID: snapshotID, Kind: input.Kind, FileName: name, ContentType: input.ContentType, Renderer: input.Renderer, RendererError: input.RendererError, RelativePath: filepath.ToSlash(filepath.Join("architecture", projectID, snapshotID, name)), ContentHash: digest, Size: int64(len(input.Data)), CreatedAt: time.Now().UTC()}, pendingPath: pending, destinationPath: destination})
	}
	return items, cleanup, nil
}

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/google/uuid"
)

var (
	ErrSkillMappingTargetInvalid      = errors.New("skill mapping target is invalid")
	ErrSkillMappingPathNotAbsolute    = errors.New("skill mapping path must be absolute")
	ErrSkillMappingSourceNested       = errors.New("skill mapping target is inside the managed Skill directory")
	ErrSkillMappingConflict           = errors.New("skill mapping conflicts with an existing file system object")
	ErrSkillMappingPermissionRequired = errors.New("creating Skill directory links requires additional Windows permission")
	ErrSkillMappingLinkInvalid        = errors.New("skill mapping link is invalid")
	ErrSkillMappingSourceInvalid      = errors.New("skill mapping source is invalid")
	ErrSkillMappingNotFound           = errors.New("skill mapping was not found")
	ErrSkillMappingSkillNotFound      = errors.New("skill mapping Skill was not found")
	ErrSkillMapped                    = errors.New("Skill is still mapped to an external directory")
)

type preparedSkillMapping struct {
	skill      model.Skill
	sourcePath string
}

type mappingInspection struct {
	status string
	err    error
	text   string
}

func (s *Store) ListSkillMappingTargets(ctx context.Context) ([]model.SkillMappingTarget, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,kind,directory_path,status,error,created_at,updated_at,last_verified_at
		FROM skill_mapping_targets ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	items := []model.SkillMappingTarget{}
	for rows.Next() {
		item, err := scanSkillMappingTarget(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range items {
		items[i].Mappings, err = s.listSkillMappings(ctx, items[i])
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) GetSkillMappingTarget(ctx context.Context, id string) (model.SkillMappingTarget, error) {
	item, err := scanSkillMappingTarget(s.DB.QueryRowContext(ctx, `SELECT id,name,kind,directory_path,status,error,created_at,updated_at,last_verified_at
		FROM skill_mapping_targets WHERE id=?`, id))
	if err != nil {
		return item, err
	}
	item.Mappings, err = s.listSkillMappings(ctx, item)
	return item, err
}

func scanSkillMappingTarget(row rowScanner) (model.SkillMappingTarget, error) {
	var item model.SkillMappingTarget
	var created, updated, verified sql.NullString
	if err := row.Scan(&item.ID, &item.Name, &item.Kind, &item.DirectoryPath, &item.Status, &item.Error, &created, &updated, &verified); err != nil {
		return item, err
	}
	item.CreatedAt = parseTime(created.String)
	item.UpdatedAt = parseTime(updated.String)
	item.LastVerifiedAt = nullableTime(verified)
	item.Mappings = []model.SkillMapping{}
	return item, nil
}

func nullableTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	t := parseTime(value.String)
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Store) listSkillMappings(ctx context.Context, target model.SkillMappingTarget) ([]model.SkillMapping, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT m.target_id,m.skill_id,s.name,s.root_path,m.link_name,m.link_path,m.status,m.error,m.created_at,m.updated_at,m.last_verified_at
		FROM skill_mappings m JOIN skills s ON s.id=m.skill_id WHERE m.target_id=? ORDER BY lower(s.name), s.id`, target.ID)
	if err != nil {
		return nil, err
	}
	items := []model.SkillMapping{}
	for rows.Next() {
		var item model.SkillMapping
		var rootPath, created, updated, verified sql.NullString
		if err := rows.Scan(&item.TargetID, &item.SkillID, &item.SkillName, &rootPath, &item.LinkName, &item.LinkPath, &item.Status, &item.Error, &created, &updated, &verified); err != nil {
			rows.Close()
			return nil, err
		}
		item.SourcePath, err = s.Resolve(rootPath.String)
		if err != nil {
			rows.Close()
			return nil, err
		}
		item.CreatedAt = parseTime(created.String)
		item.UpdatedAt = parseTime(updated.String)
		item.LastVerifiedAt = nullableTime(verified)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateSkillMappingTarget(ctx context.Context, req model.CreateSkillMappingTargetRequest) (model.SkillMappingTarget, error) {
	name, kind, directoryPath, err := validateSkillMappingTarget(s, req.Name, req.Kind, req.DirectoryPath)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	skills, err := s.prepareSkillMappings(ctx, req.SkillIDs)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	var existing string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM skill_mapping_targets WHERE directory_path=?`, directoryPath).Scan(&existing); err == nil {
		return model.SkillMappingTarget{}, fmt.Errorf("%w: target already exists", ErrSkillMappingConflict)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.SkillMappingTarget{}, err
	}

	id := uuid.NewString()
	_, stamp := now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_mapping_targets(id,name,kind,directory_path,status,error,created_at,updated_at,last_verified_at)
		VALUES(?,?,?,?,?,?,?, ?, ?)`, id, name, kind, directoryPath, "pending", "", stamp, stamp, stamp); err != nil {
		return model.SkillMappingTarget{}, err
	}
	for _, prepared := range skills {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_mappings(target_id,skill_id,link_name,link_path,status,error,created_at,updated_at,last_verified_at)
			VALUES(?,?,?,?,?,?,?,?,?)`, id, prepared.skill.ID, prepared.skill.Name, filepath.ToSlash(prepared.skill.Name), "pending", "", stamp, stamp, stamp); err != nil {
			return model.SkillMappingTarget{}, err
		}
	}
	created := []preparedSkillMapping{}
	for _, prepared := range skills {
		wasCreated, err := createOwnedSkillLink(directoryPath, prepared.skill.Name, prepared.sourcePath)
		if err != nil {
			for _, createdSkill := range created {
				_ = removeOwnedSkillLink(directoryPath, createdSkill.skill.Name, createdSkill.sourcePath)
			}
			return model.SkillMappingTarget{}, err
		}
		if wasCreated {
			created = append(created, prepared)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skill_mappings SET status='ready',error='',updated_at=?,last_verified_at=? WHERE target_id=? AND skill_id=?`, stamp, stamp, id, prepared.skill.ID); err != nil {
			for _, createdSkill := range created {
				_ = removeOwnedSkillLink(directoryPath, createdSkill.skill.Name, createdSkill.sourcePath)
			}
			return model.SkillMappingTarget{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_mapping_targets SET status='ready',error='',updated_at=?,last_verified_at=? WHERE id=?`, stamp, stamp, id); err != nil {
		for _, createdSkill := range created {
			_ = removeOwnedSkillLink(directoryPath, createdSkill.skill.Name, createdSkill.sourcePath)
		}
		return model.SkillMappingTarget{}, err
	}
	if err := tx.Commit(); err != nil {
		for _, createdSkill := range created {
			_ = removeOwnedSkillLink(directoryPath, createdSkill.skill.Name, createdSkill.sourcePath)
		}
		return model.SkillMappingTarget{}, err
	}
	return s.GetSkillMappingTarget(ctx, id)
}

func (s *Store) AddSkillMappings(ctx context.Context, targetID string, skillIDs []string) (model.SkillMappingTarget, error) {
	target, err := s.GetSkillMappingTarget(ctx, targetID)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	if err := requireSkillMappingTarget(target.DirectoryPath); err != nil {
		return model.SkillMappingTarget{}, err
	}
	prepared, err := s.prepareSkillMappings(ctx, skillIDs)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	newSkills := []preparedSkillMapping{}
	for _, item := range prepared {
		var count int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_mappings WHERE target_id=? AND skill_id=?`, targetID, item.skill.ID).Scan(&count); err != nil {
			return model.SkillMappingTarget{}, err
		}
		if count == 0 {
			newSkills = append(newSkills, item)
		}
	}
	if len(newSkills) == 0 {
		return target, nil
	}

	_, stamp := now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	defer tx.Rollback()
	for _, prepared := range newSkills {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_mappings(target_id,skill_id,link_name,link_path,status,error,created_at,updated_at,last_verified_at)
			VALUES(?,?,?,?,?,?,?,?,?)`, targetID, prepared.skill.ID, prepared.skill.Name, filepath.ToSlash(prepared.skill.Name), "pending", "", stamp, stamp, stamp); err != nil {
			return model.SkillMappingTarget{}, err
		}
	}
	created := []preparedSkillMapping{}
	for _, prepared := range newSkills {
		wasCreated, err := createOwnedSkillLink(target.DirectoryPath, prepared.skill.Name, prepared.sourcePath)
		if err != nil {
			for _, createdSkill := range created {
				_ = removeOwnedSkillLink(target.DirectoryPath, createdSkill.skill.Name, createdSkill.sourcePath)
			}
			return model.SkillMappingTarget{}, err
		}
		if wasCreated {
			created = append(created, prepared)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skill_mappings SET status='ready',error='',updated_at=?,last_verified_at=? WHERE target_id=? AND skill_id=?`, stamp, stamp, targetID, prepared.skill.ID); err != nil {
			for _, createdSkill := range created {
				_ = removeOwnedSkillLink(target.DirectoryPath, createdSkill.skill.Name, createdSkill.sourcePath)
			}
			return model.SkillMappingTarget{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_mapping_targets SET status='ready',error='',updated_at=?,last_verified_at=? WHERE id=?`, stamp, stamp, targetID); err != nil {
		for _, createdSkill := range created {
			_ = removeOwnedSkillLink(target.DirectoryPath, createdSkill.skill.Name, createdSkill.sourcePath)
		}
		return model.SkillMappingTarget{}, err
	}
	if err := tx.Commit(); err != nil {
		for _, createdSkill := range created {
			_ = removeOwnedSkillLink(target.DirectoryPath, createdSkill.skill.Name, createdSkill.sourcePath)
		}
		return model.SkillMappingTarget{}, err
	}
	return s.GetSkillMappingTarget(ctx, targetID)
}

func (s *Store) UpdateSkillMappingTarget(ctx context.Context, id string, req model.UpdateSkillMappingTargetRequest) (model.SkillMappingTarget, error) {
	if _, err := s.GetSkillMappingTarget(ctx, id); err != nil {
		return model.SkillMappingTarget{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 200 {
		return model.SkillMappingTarget{}, fmt.Errorf("%w: name is required and must be at most 200 characters", ErrSkillMappingTargetInvalid)
	}
	kind := strings.TrimSpace(req.Kind)
	if kind != "agent" && kind != "project" {
		return model.SkillMappingTarget{}, fmt.Errorf("%w: kind must be agent or project", ErrSkillMappingTargetInvalid)
	}
	_, stamp := now()
	if _, err := s.DB.ExecContext(ctx, `UPDATE skill_mapping_targets SET name=?,kind=?,updated_at=? WHERE id=?`, name, kind, stamp, id); err != nil {
		return model.SkillMappingTarget{}, err
	}
	return s.GetSkillMappingTarget(ctx, id)
}

func (s *Store) VerifySkillMappingTarget(ctx context.Context, id string) (model.SkillMappingTarget, error) {
	target, err := s.GetSkillMappingTarget(ctx, id)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	targetStatus, targetErr := readSkillMappingTargetState(target.DirectoryPath)
	_, stamp := now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	defer tx.Rollback()
	firstError := targetErr
	statuses := []string{}
	for _, mapping := range target.Mappings {
		inspection := mappingInspection{status: targetStatus}
		if targetStatus == "ready" {
			inspection = inspectSkillMappingLink(target.DirectoryPath, mapping.LinkPath, mapping.SourcePath)
		}
		if inspection.err != nil && firstError == nil {
			firstError = inspection.err
		}
		statuses = append(statuses, inspection.status)
		if _, err := tx.ExecContext(ctx, `UPDATE skill_mappings SET status=?,error=?,updated_at=?,last_verified_at=? WHERE target_id=? AND skill_id=?`, inspection.status, inspection.text, stamp, stamp, id, mapping.SkillID); err != nil {
			return model.SkillMappingTarget{}, err
		}
	}
	if targetStatus == "ready" {
		targetStatus = mappingTargetStatus(statuses)
		if targetStatus == "ready" {
			firstError = nil
		}
	}
	errorText := ""
	if firstError != nil {
		errorText = firstError.Error()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_mapping_targets SET status=?,error=?,updated_at=?,last_verified_at=? WHERE id=?`, targetStatus, errorText, stamp, stamp, id); err != nil {
		return model.SkillMappingTarget{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.SkillMappingTarget{}, err
	}
	return s.GetSkillMappingTarget(ctx, id)
}

func (s *Store) RepairSkillMapping(ctx context.Context, targetID, skillID string) (model.SkillMappingTarget, error) {
	target, err := s.GetSkillMappingTarget(ctx, targetID)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	var mapping *model.SkillMapping
	for i := range target.Mappings {
		if target.Mappings[i].SkillID == skillID {
			mapping = &target.Mappings[i]
			break
		}
	}
	if mapping == nil {
		return model.SkillMappingTarget{}, ErrSkillMappingNotFound
	}
	if err := requireSkillMappingTarget(target.DirectoryPath); err != nil {
		return model.SkillMappingTarget{}, err
	}
	inspection := inspectSkillMappingLink(target.DirectoryPath, mapping.LinkPath, mapping.SourcePath)
	if inspection.status == "ready" {
		return target, nil
	}
	if inspection.status != "missing" {
		if inspection.err != nil {
			return model.SkillMappingTarget{}, inspection.err
		}
		return model.SkillMappingTarget{}, ErrSkillMappingConflict
	}
	if _, err := createOwnedSkillLink(target.DirectoryPath, mapping.LinkName, mapping.SourcePath); err != nil {
		return model.SkillMappingTarget{}, err
	}
	return s.VerifySkillMappingTarget(ctx, targetID)
}

func (s *Store) RemoveSkillMapping(ctx context.Context, targetID, skillID string) (model.SkillMappingTarget, error) {
	target, err := s.GetSkillMappingTarget(ctx, targetID)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	var mapping *model.SkillMapping
	for i := range target.Mappings {
		if target.Mappings[i].SkillID == skillID {
			mapping = &target.Mappings[i]
			break
		}
	}
	if mapping == nil {
		return model.SkillMappingTarget{}, ErrSkillMappingNotFound
	}
	targetStatus, targetErr := readSkillMappingTargetState(target.DirectoryPath)
	if targetStatus != "missing" {
		if targetErr != nil {
			return model.SkillMappingTarget{}, targetErr
		}
		inspection := inspectSkillMappingLink(target.DirectoryPath, mapping.LinkPath, mapping.SourcePath)
		if inspection.status == "ready" {
			if err := removeOwnedSkillLink(target.DirectoryPath, mapping.LinkName, mapping.SourcePath); err != nil {
				return model.SkillMappingTarget{}, err
			}
		} else if inspection.status != "missing" {
			if inspection.err != nil {
				return model.SkillMappingTarget{}, inspection.err
			}
			return model.SkillMappingTarget{}, ErrSkillMappingConflict
		}
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM skill_mappings WHERE target_id=? AND skill_id=?`, targetID, skillID); err != nil {
		return model.SkillMappingTarget{}, err
	}
	return s.VerifySkillMappingTarget(ctx, targetID)
}

func (s *Store) ForgetSkillMapping(ctx context.Context, targetID, skillID string) (model.SkillMappingTarget, error) {
	if _, err := s.GetSkillMappingTarget(ctx, targetID); err != nil {
		return model.SkillMappingTarget{}, err
	}
	result, err := s.DB.ExecContext(ctx, `DELETE FROM skill_mappings WHERE target_id=? AND skill_id=?`, targetID, skillID)
	if err != nil {
		return model.SkillMappingTarget{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return model.SkillMappingTarget{}, ErrSkillMappingNotFound
	}
	return s.VerifySkillMappingTarget(ctx, targetID)
}

func (s *Store) DeleteSkillMappingTarget(ctx context.Context, id string) error {
	target, err := s.GetSkillMappingTarget(ctx, id)
	if err != nil {
		return err
	}
	targetStatus, targetErr := readSkillMappingTargetState(target.DirectoryPath)
	if targetStatus != "missing" {
		if targetErr != nil {
			return targetErr
		}
		for _, mapping := range target.Mappings {
			inspection := inspectSkillMappingLink(target.DirectoryPath, mapping.LinkPath, mapping.SourcePath)
			if inspection.status != "ready" && inspection.status != "missing" {
				if inspection.err != nil {
					return inspection.err
				}
				return ErrSkillMappingConflict
			}
		}
		for _, mapping := range target.Mappings {
			inspection := inspectSkillMappingLink(target.DirectoryPath, mapping.LinkPath, mapping.SourcePath)
			if inspection.status == "ready" {
				if err := removeOwnedSkillLink(target.DirectoryPath, mapping.LinkName, mapping.SourcePath); err != nil {
					return err
				}
			}
		}
	}
	result, err := s.DB.ExecContext(ctx, `DELETE FROM skill_mapping_targets WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) prepareSkillMappings(ctx context.Context, ids []string) ([]preparedSkillMapping, error) {
	prepared := []preparedSkillMapping{}
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		skill, err := s.GetSkill(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("%w: %s", ErrSkillMappingSkillNotFound, id)
			}
			return nil, err
		}
		if skill.Status != "ready" {
			return nil, fmt.Errorf("%w: Skill %s is not ready", ErrSkillMappingSourceInvalid, skill.Name)
		}
		sourcePath, err := s.Resolve(skill.RootPath)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSkillMappingSourceInvalid, err)
		}
		info, err := os.Lstat(sourcePath)
		if err != nil || !info.IsDir() {
			if err != nil && isMappingPermissionError(err) {
				return nil, fmt.Errorf("%w: %v", ErrSkillMappingPermissionRequired, err)
			}
			return nil, fmt.Errorf("%w: Skill source directory is unavailable", ErrSkillMappingSourceInvalid)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: Skill source directory cannot be a symbolic link", ErrSkillMappingSourceInvalid)
		}
		if err := validateSkillLinkName(skill.Name); err != nil {
			return nil, err
		}
		prepared = append(prepared, preparedSkillMapping{skill: skill, sourcePath: sourcePath})
	}
	return prepared, nil
}

func validateSkillMappingTarget(s *Store, name, kind, directoryPath string) (string, string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return "", "", "", fmt.Errorf("%w: name is required and must be at most 200 characters", ErrSkillMappingTargetInvalid)
	}
	kind = strings.TrimSpace(kind)
	if kind != "agent" && kind != "project" {
		return "", "", "", fmt.Errorf("%w: kind must be agent or project", ErrSkillMappingTargetInvalid)
	}
	directoryPath, err := normalizeSkillMappingPath(directoryPath)
	if err != nil {
		return "", "", "", err
	}
	dataSkills, err := filepath.Abs(filepath.Join(s.DataRoot, "skills"))
	if err != nil {
		return "", "", "", err
	}
	if pathWithin(dataSkills, directoryPath) {
		return "", "", "", ErrSkillMappingSourceNested
	}
	if err := requireSkillMappingTarget(directoryPath); err != nil {
		return "", "", "", err
	}
	if runtime.GOOS != "windows" {
		return "", "", "", fmt.Errorf("%w: external Skill mappings are supported on Windows only", ErrSkillMappingTargetInvalid)
	}
	return name, kind, directoryPath, nil
}

func normalizeSkillMappingPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: directoryPath is required", ErrSkillMappingTargetInvalid)
	}
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("%w: directoryPath contains an invalid character", ErrSkillMappingTargetInvalid)
	}
	if !filepath.IsAbs(value) {
		return "", ErrSkillMappingPathNotAbsolute
	}
	return filepath.Abs(filepath.Clean(value))
}

func requireSkillMappingTarget(path string) error {
	status, err := readSkillMappingTargetState(path)
	if status == "ready" {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: target directory is %s", ErrSkillMappingTargetInvalid, status)
}

func readSkillMappingTargetState(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", fmt.Errorf("%w: target directory does not exist", ErrSkillMappingTargetInvalid)
	}
	if err != nil {
		if isMappingPermissionError(err) {
			return "permission_required", fmt.Errorf("%w: %v", ErrSkillMappingPermissionRequired, err)
		}
		return "invalid", fmt.Errorf("%w: cannot inspect target directory: %v", ErrSkillMappingTargetInvalid, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "invalid", fmt.Errorf("%w: target directory cannot be a symbolic link", ErrSkillMappingTargetInvalid)
	}
	if !info.IsDir() {
		return "invalid", fmt.Errorf("%w: target path is not a directory", ErrSkillMappingTargetInvalid)
	}
	return "ready", nil
}

func validateSkillLinkName(value string) error {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\\:`) || filepath.Base(value) != value {
		return fmt.Errorf("%w: Skill name cannot be used as a directory link name", ErrSkillMappingLinkInvalid)
	}
	return nil
}

func inspectSkillMappingLink(targetDir, linkPath, sourcePath string) mappingInspection {
	if err := validateSkillLinkName(linkPath); err != nil {
		return mappingInspection{status: "invalid", err: err, text: err.Error()}
	}
	info, err := os.Lstat(sourcePath)
	if err != nil || !info.IsDir() {
		if err != nil && isMappingPermissionError(err) {
			wrapped := fmt.Errorf("%w: %v", ErrSkillMappingPermissionRequired, err)
			return mappingInspection{status: "permission_required", err: wrapped, text: wrapped.Error()}
		}
		wrapped := fmt.Errorf("%w: Skill source directory is unavailable", ErrSkillMappingSourceInvalid)
		return mappingInspection{status: "invalid", err: wrapped, text: wrapped.Error()}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		wrapped := fmt.Errorf("%w: Skill source directory cannot be a symbolic link", ErrSkillMappingSourceInvalid)
		return mappingInspection{status: "invalid", err: wrapped, text: wrapped.Error()}
	}
	fullPath := filepath.Join(targetDir, filepath.FromSlash(linkPath))
	if !pathEqual(filepath.Dir(fullPath), targetDir) {
		wrapped := fmt.Errorf("%w: link path escapes target directory", ErrSkillMappingLinkInvalid)
		return mappingInspection{status: "invalid", err: wrapped, text: wrapped.Error()}
	}
	info, err = os.Lstat(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return mappingInspection{status: "missing", text: "link is missing"}
	}
	if err != nil {
		if isMappingPermissionError(err) {
			wrapped := fmt.Errorf("%w: %v", ErrSkillMappingPermissionRequired, err)
			return mappingInspection{status: "permission_required", err: wrapped, text: wrapped.Error()}
		}
		wrapped := fmt.Errorf("%w: cannot inspect link: %v", ErrSkillMappingLinkInvalid, err)
		return mappingInspection{status: "invalid", err: wrapped, text: wrapped.Error()}
	}
	if info.Mode()&os.ModeSymlink == 0 {
		wrapped := fmt.Errorf("%w: link path is occupied by a regular file or directory", ErrSkillMappingConflict)
		return mappingInspection{status: "conflict", err: wrapped, text: wrapped.Error()}
	}
	linkTarget, err := os.Readlink(fullPath)
	if err != nil {
		if isMappingPermissionError(err) {
			wrapped := fmt.Errorf("%w: %v", ErrSkillMappingPermissionRequired, err)
			return mappingInspection{status: "permission_required", err: wrapped, text: wrapped.Error()}
		}
		wrapped := fmt.Errorf("%w: cannot read link target: %v", ErrSkillMappingLinkInvalid, err)
		return mappingInspection{status: "invalid", err: wrapped, text: wrapped.Error()}
	}
	if !filepath.IsAbs(linkTarget) {
		linkTarget = filepath.Join(filepath.Dir(fullPath), linkTarget)
	}
	linkTarget, err = filepath.Abs(filepath.Clean(linkTarget))
	if err != nil {
		wrapped := fmt.Errorf("%w: cannot normalize link target: %v", ErrSkillMappingLinkInvalid, err)
		return mappingInspection{status: "invalid", err: wrapped, text: wrapped.Error()}
	}
	if pathEqual(linkTarget, sourcePath) {
		return mappingInspection{status: "ready"}
	}
	wrapped := fmt.Errorf("%w: link points outside the managed Skill source", ErrSkillMappingConflict)
	return mappingInspection{status: "conflict", err: wrapped, text: wrapped.Error()}
}

func createOwnedSkillLink(targetDir, linkName, sourcePath string) (bool, error) {
	if err := validateSkillLinkName(linkName); err != nil {
		return false, err
	}
	inspection := inspectSkillMappingLink(targetDir, linkName, sourcePath)
	switch inspection.status {
	case "ready":
		return false, nil
	case "missing":
		fullPath := filepath.Join(targetDir, filepath.FromSlash(linkName))
		if err := os.Symlink(sourcePath, fullPath); err != nil {
			if isMappingPermissionError(err) {
				return false, fmt.Errorf("%w: %v", ErrSkillMappingPermissionRequired, err)
			}
			if !errors.Is(err, os.ErrExist) {
				return false, fmt.Errorf("%w: %v", ErrSkillMappingConflict, err)
			}
		}
		verify := inspectSkillMappingLink(targetDir, linkName, sourcePath)
		if verify.status == "ready" {
			return true, nil
		}
		if verify.err != nil {
			return false, verify.err
		}
		return false, ErrSkillMappingConflict
	default:
		if inspection.err != nil {
			return false, inspection.err
		}
		return false, ErrSkillMappingConflict
	}
}

func removeOwnedSkillLink(targetDir, linkName, sourcePath string) error {
	inspection := inspectSkillMappingLink(targetDir, linkName, sourcePath)
	if inspection.status == "missing" {
		return nil
	}
	if inspection.status != "ready" {
		if inspection.err != nil {
			return inspection.err
		}
		return ErrSkillMappingConflict
	}
	fullPath := filepath.Join(targetDir, filepath.FromSlash(linkName))
	if err := os.Remove(fullPath); err != nil {
		if isMappingPermissionError(err) {
			return fmt.Errorf("%w: %v", ErrSkillMappingPermissionRequired, err)
		}
		return err
	}
	return nil
}

func mappingTargetStatus(statuses []string) string {
	for _, status := range []string{"permission_required", "conflict", "invalid", "partial"} {
		for _, current := range statuses {
			if current == status || (status == "partial" && current == "missing") {
				return status
			}
		}
	}
	return "ready"
}

func pathWithin(root, target string) bool {
	root = mappingComparablePath(root)
	target = mappingComparablePath(target)
	if pathEqual(root, target) {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func pathEqual(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func mappingComparablePath(value string) string {
	value = filepath.Clean(value)
	if runtime.GOOS == "windows" {
		return strings.ToLower(value)
	}
	return value
}

func isMappingPermissionError(err error) bool {
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == syscall.Errno(1314) || errno == syscall.Errno(5))
}

package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

const SubmissionFormatterRole = "knowledge-submission-formatter"

var ErrSystemSkillProtected = errors.New("system Skill cannot be modified")

//go:embed submission_formatter.md
var submissionFormatterMarkdown []byte

func (s *Store) EnsureSubmissionFormatter(ctx context.Context) (model.Skill, error) {
	var currentID, currentHash string
	err := s.DB.QueryRowContext(ctx, `SELECT skill_id,content_hash FROM system_skills WHERE role=?`, SubmissionFormatterRole).Scan(&currentID, &currentHash)
	if err == nil {
		if _, err := s.GetSkill(ctx, currentID); err == nil && currentHash == systemSkillHash(submissionFormatterMarkdown) {
			return s.GetSystemSkill(ctx, SubmissionFormatterRole)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.Skill{}, err
	}

	name := "kah-knowledge-submission-formatter"
	var existingID string
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM skills WHERE name=?`, name).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		existingID = ""
	} else if err != nil {
		return model.Skill{}, err
	}
	if currentID == "" && existingID != "" {
		return model.Skill{}, fmt.Errorf("reserved formatter Skill name %q is already occupied", name)
	}

	temp, err := os.CreateTemp(filepath.Join(s.DataRoot, "staging"), "submission-formatter-*.md")
	if err != nil {
		return model.Skill{}, err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(submissionFormatterMarkdown); err != nil {
		temp.Close()
		return model.Skill{}, err
	}
	if err := temp.Close(); err != nil {
		return model.Skill{}, err
	}
	item, err := s.ImportSkill(ctx, tempName, existingID != "")
	if err != nil {
		return model.Skill{}, err
	}
	_, stamp := now()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO system_skills(role,skill_id,content_hash,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(role) DO UPDATE SET skill_id=excluded.skill_id,content_hash=excluded.content_hash,updated_at=excluded.updated_at`, SubmissionFormatterRole, item.ID, item.ContentHash, stamp, stamp)
	if err != nil {
		return model.Skill{}, err
	}
	return s.GetSystemSkill(ctx, SubmissionFormatterRole)
}

func systemSkillHash(content []byte) string {
	fileHash := sha256.Sum256(content)
	aggregate := sha256.New()
	_, _ = fmt.Fprintf(aggregate, "SKILL.md:%d:%x\n", len(content), fileHash)
	return fmt.Sprintf("%x", aggregate.Sum(nil))
}

func (s *Store) GetSystemSkill(ctx context.Context, role string) (model.Skill, error) {
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT skill_id FROM system_skills WHERE role=?`, role).Scan(&id); err != nil {
		return model.Skill{}, err
	}
	item, err := s.GetSkill(ctx, id)
	if err != nil {
		return model.Skill{}, err
	}
	item.SystemRole = role
	return item, nil
}

func (s *Store) SystemSkillRole(ctx context.Context, skillID string) (string, error) {
	var role string
	err := s.DB.QueryRowContext(ctx, `SELECT role FROM system_skills WHERE skill_id=?`, skillID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (s *Store) IsSystemSkill(ctx context.Context, skillID string) (bool, error) {
	role, err := s.SystemSkillRole(ctx, skillID)
	return role != "", err
}

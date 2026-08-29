package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func importMappingTestSkill(t *testing.T, store *Store, name string) model.Skill {
	t.Helper()
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "SKILL.md")
	content := "---\nname: " + name + "\ndescription: mapping test skill\n---\n\n# " + name + "\n"
	if err := os.WriteFile(source, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	skill, err := store.ImportSkill(context.Background(), source, false)
	if err != nil {
		t.Fatal(err)
	}
	return skill
}

func skipIfSymlinkPermission(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, ErrSkillMappingPermissionRequired) {
		t.Skip("Windows directory symbolic links are not permitted in this test environment")
	}
}

func TestSkillMappingLifecycleAndDeleteProtection(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	skill := importMappingTestSkill(t, store, "mapping-lifecycle")
	targetPath := t.TempDir()
	target, err := store.CreateSkillMappingTarget(context.Background(), model.CreateSkillMappingTargetRequest{
		Name: "Test Agent", Kind: "agent", DirectoryPath: targetPath, SkillIDs: []string{skill.ID},
	})
	if err != nil {
		skipIfSymlinkPermission(t, err)
		t.Fatal(err)
	}
	if target.Status != "ready" || len(target.Mappings) != 1 || target.Mappings[0].Status != "ready" {
		t.Fatalf("unexpected created target: %+v", target)
	}
	linkPath := filepath.Join(targetPath, skill.Name)
	info, err := os.Lstat(linkPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected directory symlink, info=%v err=%v", info, err)
	}
	linkTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath, err := store.Resolve(skill.RootPath)
	if err != nil || !pathEqual(linkTarget, sourcePath) {
		t.Fatalf("link target = %q, source = %q, err = %v", linkTarget, sourcePath, err)
	}

	if err := store.DeleteSkill(context.Background(), skill.ID); !errors.Is(err, ErrSkillMapped) {
		t.Fatalf("delete mapped Skill error = %v", err)
	}
	if _, err := store.AddSkillMappings(context.Background(), target.ID, []string{skill.ID}); err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	verified, err := store.VerifySkillMappingTarget(context.Background(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Status != "partial" || verified.Mappings[0].Status != "missing" {
		t.Fatalf("missing link verification: %+v", verified)
	}
	repaired, err := store.RepairSkillMapping(context.Background(), target.ID, skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Status != "ready" || repaired.Mappings[0].Status != "ready" {
		t.Fatalf("repaired target: %+v", repaired)
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linkPath, []byte("external object"), 0o640); err != nil {
		t.Fatal(err)
	}
	conflicted, err := store.VerifySkillMappingTarget(context.Background(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if conflicted.Status != "conflict" || conflicted.Mappings[0].Status != "conflict" {
		t.Fatalf("conflict verification: %+v", conflicted)
	}
	if _, err := store.ForgetSkillMapping(context.Background(), target.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(linkPath); err != nil {
		t.Fatalf("forget should preserve external object: %v", err)
	}
	if _, err := store.RemoveSkillMapping(context.Background(), target.ID, skill.ID); !errors.Is(err, ErrSkillMappingNotFound) {
		t.Fatal(err)
	}
	if err := store.DeleteSkillMappingTarget(context.Background(), target.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSkill(context.Background(), skill.ID); err != nil {
		t.Fatalf("delete unlinked Skill: %v", err)
	}
}

func TestSkillMappingRejectsNestedAndConflictingTargets(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	skill := importMappingTestSkill(t, store, "mapping-conflict")
	skillsRoot, err := filepath.Abs(filepath.Join(store.DataRoot, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSkillMappingTarget(context.Background(), model.CreateSkillMappingTargetRequest{Name: "Nested", Kind: "agent", DirectoryPath: skillsRoot, SkillIDs: []string{skill.ID}}); !errors.Is(err, ErrSkillMappingSourceNested) {
		t.Fatalf("nested target error = %v", err)
	}
	if _, err := store.CreateSkillMappingTarget(context.Background(), model.CreateSkillMappingTargetRequest{Name: "Relative", Kind: "agent", DirectoryPath: ".\\skills", SkillIDs: []string{skill.ID}}); !errors.Is(err, ErrSkillMappingPathNotAbsolute) {
		t.Fatalf("relative target error = %v", err)
	}
	targetPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(targetPath, skill.Name), 0o750); err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateSkillMappingTarget(context.Background(), model.CreateSkillMappingTargetRequest{Name: "Conflict", Kind: "project", DirectoryPath: targetPath, SkillIDs: []string{skill.ID}})
	skipIfSymlinkPermission(t, err)
	if !errors.Is(err, ErrSkillMappingConflict) {
		t.Fatalf("conflicting object error = %v", err)
	}
	items, err := store.ListSkillMappingTargets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("failed creation left targets: %+v", items)
	}
}

func TestSkillMappingBatchRollback(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := importMappingTestSkill(t, store, "mapping-first")
	second := importMappingTestSkill(t, store, "mapping-second")
	targetPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetPath, second.Name), []byte("owned by someone else"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateSkillMappingTarget(context.Background(), model.CreateSkillMappingTargetRequest{Name: "Rollback", Kind: "project", DirectoryPath: targetPath, SkillIDs: []string{first.ID, second.ID}})
	skipIfSymlinkPermission(t, err)
	if !errors.Is(err, ErrSkillMappingConflict) {
		t.Fatalf("batch error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(targetPath, first.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("batch rollback left first link, err=%v", err)
	}
	items, err := store.ListSkillMappingTargets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("batch rollback left target records: %+v", items)
	}
}

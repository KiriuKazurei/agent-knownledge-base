package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestSkillMappingDesktopAPILifecycle(t *testing.T) {
	server, handler := testServer(t)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "SKILL.md")
	content := "---\nname: api-mapping-skill\ndescription: mapping API test\n---\n\n# API Mapping\n"
	if err := os.WriteFile(source, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	skill, err := server.Store.ImportSkill(context.Background(), source, false)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := t.TempDir()
	created := request(t, handler, http.MethodPost, "/api/v1/skill-mapping-targets", model.CreateSkillMappingTargetRequest{
		Name: "API Agent", Kind: "agent", DirectoryPath: targetPath, SkillIDs: []string{skill.ID},
	}, "desktop-test")
	if containsText(created.Body.String(), "skill_mapping_permission_required") {
		t.Skip("Windows directory symbolic links are not permitted in this test environment")
	}
	if created.Code != http.StatusCreated {
		t.Fatalf("create mapping target: %d %s", created.Code, created.Body.String())
	}
	var target model.SkillMappingTarget
	if err := json.Unmarshal(created.Body.Bytes(), &target); err != nil {
		t.Fatal(err)
	}
	if target.ID == "" || target.Status != "ready" || len(target.Mappings) != 1 {
		t.Fatalf("created target: %+v", target)
	}

	list := request(t, handler, http.MethodGet, "/api/v1/skill-mapping-targets", nil, "desktop-test")
	if list.Code != http.StatusOK || !containsText(list.Body.String(), target.ID) {
		t.Fatalf("list mappings: %d %s", list.Code, list.Body.String())
	}
	unauthorized := request(t, handler, http.MethodGet, "/api/v1/skill-mapping-targets", nil, "not-desktop")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("mapping endpoint auth: %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	deleteSkill := request(t, handler, http.MethodDelete, "/api/v1/skills/"+skill.ID, nil, "desktop-test")
	if deleteSkill.Code != http.StatusConflict || !containsText(deleteSkill.Body.String(), "skill_mapping_skill_in_use") {
		t.Fatalf("mapped Skill delete: %d %s", deleteSkill.Code, deleteSkill.Body.String())
	}
	remove := request(t, handler, http.MethodDelete, "/api/v1/skill-mapping-targets/"+target.ID+"/skills/"+skill.ID, nil, "desktop-test")
	if remove.Code != http.StatusNoContent {
		t.Fatalf("remove mapping: %d %s", remove.Code, remove.Body.String())
	}
	deleteTarget := request(t, handler, http.MethodDelete, "/api/v1/skill-mapping-targets/"+target.ID, nil, "desktop-test")
	if deleteTarget.Code != http.StatusNoContent {
		t.Fatalf("delete target: %d %s", deleteTarget.Code, deleteTarget.Body.String())
	}
	deleteSkill = request(t, handler, http.MethodDelete, "/api/v1/skills/"+skill.ID, nil, "desktop-test")
	if deleteSkill.Code != http.StatusNoContent {
		t.Fatalf("unmapped Skill delete: %d %s", deleteSkill.Code, deleteSkill.Body.String())
	}
}

func containsText(value, needle string) bool {
	return strings.Contains(value, needle)
}

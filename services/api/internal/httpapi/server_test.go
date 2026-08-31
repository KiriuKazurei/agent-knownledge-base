package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/config"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
)

func testServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := New(config.Config{DesktopToken: "desktop-test", Version: "test"}, store, nil, nil)
	return server, server.Router()
}

func request(t *testing.T, handler http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &payload)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestHealthAndLibraryLifecycle(t *testing.T) {
	_, handler := testServer(t)
	health := request(t, handler, "GET", "/api/v1/health", nil, "")
	if health.Code != 200 {
		t.Fatalf("health: %d %s", health.Code, health.Body.String())
	}
	created := request(t, handler, "POST", "/api/v1/libraries", map[string]any{"name": "Knowledge"}, "desktop-test")
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	listed := request(t, handler, "GET", "/api/v1/libraries", nil, "desktop-test")
	if listed.Code != 200 {
		t.Fatalf("list: %d", listed.Code)
	}
}

func TestManagementRequiresDesktop(t *testing.T) {
	_, handler := testServer(t)
	response := request(t, handler, "GET", "/api/v1/libraries", nil, "bad-token")
	if response.Code != 401 {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestCreateBackupHTTPProducesVerifiedArchive(t *testing.T) {
	server, handler := testServer(t)
	response := request(t, handler, "POST", "/api/v1/backups", map[string]any{"includeIndexes": true}, "desktop-test")
	if response.Code != http.StatusCreated {
		t.Fatalf("backup: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := server.Store.VerifyBackup(context.Background(), body.Path)
	if err != nil {
		t.Fatalf("backup verification: %v", err)
	}
	if digest != body.SHA256 || !manifest.IncludeIndexes {
		t.Fatalf("unexpected verified backup: digest=%s manifest=%+v", digest, manifest)
	}
}

func TestSkillAgentEndpoints(t *testing.T) {
	t.Skip("superseded: Agent Skill discovery is now provided by MCP resources")
	server, handler := testServer(t)
	created := request(t, handler, "POST", "/api/v1/libraries", map[string]any{"name": "Skill Library"}, "desktop-test")
	if created.Code != http.StatusCreated {
		t.Fatalf("create library: %d %s", created.Code, created.Body.String())
	}
	var library struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &library); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "SKILL.md")
	content := "---\nname: data-analysis\ndescription: Analyze datasets and create data reports.\n---\n\n# Data Analysis\n\nUse the linked knowledge.\n"
	if err := os.WriteFile(source, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	item, err := server.Store.ImportSkill(context.Background(), source, false)
	if err != nil {
		t.Fatal(err)
	}
	links := request(t, handler, "PUT", "/api/v1/skills/"+item.ID+"/links", map[string]any{"usesLibraryIds": []string{library.ID}, "requiresLibraryIds": []string{library.ID}}, "desktop-test")
	if links.Code != http.StatusOK {
		t.Fatalf("links: %d %s", links.Code, links.Body.String())
	}
	query := request(t, handler, "POST", "/api/v1/skills/query", map[string]any{"query": "analyze datasets", "libraryIds": []string{library.ID}}, "desktop-test")
	if query.Code != http.StatusOK || !bytes.Contains(query.Body.Bytes(), []byte("data-analysis")) {
		t.Fatalf("skill query: %d %s", query.Code, query.Body.String())
	}
	manifest := request(t, handler, "GET", "/api/v1/skills/"+item.ID+"/manifest", nil, "desktop-test")
	if manifest.Code != http.StatusOK || !bytes.Contains(manifest.Body.Bytes(), []byte("SKILL.md")) || !bytes.Contains(manifest.Body.Bytes(), []byte("# Data Analysis")) {
		t.Fatalf("manifest: %d %s", manifest.Code, manifest.Body.String())
	}
	file := request(t, handler, "GET", "/api/v1/skills/"+item.ID+"/files/SKILL.md", nil, "desktop-test")
	if file.Code != http.StatusOK || file.Body.String() != content {
		t.Fatalf("file: %d %s", file.Code, file.Body.String())
	}
	required := request(t, handler, "POST", "/api/v1/query", map[string]any{"query": "missing knowledge", "libraryIds": []string{library.ID}}, "desktop-test")
	if required.Code != http.StatusOK || !bytes.Contains(required.Body.Bytes(), []byte("requiredSkills")) {
		t.Fatalf("knowledge query required skills: %d %s", required.Code, required.Body.String())
	}
}
func TestSkillImportHTTPJobLifecycle(t *testing.T) {
	_, handler := testServer(t)
	root := t.TempDir()

	markdownPath := filepath.Join(root, "markdown-skill.md")
	markdownContent := "---\nname: http-markdown-skill\ndescription: Import a Markdown Skill through the management API.\n---\n\n# HTTP Markdown Skill\n"
	if err := os.WriteFile(markdownPath, []byte(markdownContent), 0o640); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(root, "zip-skill.zip")
	archiveFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(archiveFile)
	entries := map[string]string{
		"zip-skill/SKILL.md":            "---\nname: http-zip-skill\ndescription: Import a ZIP Skill through the management API.\n---\n\n# HTTP ZIP Skill\n",
		"zip-skill/references/guide.md": "# Guide\n",
	}
	for path, content := range entries {
		entry, createErr := archive.Create(path)
		if createErr != nil {
			archiveFile.Close()
			t.Fatal(createErr)
		}
		if _, writeErr := entry.Write([]byte(content)); writeErr != nil {
			archiveFile.Close()
			t.Fatal(writeErr)
		}
	}
	if err := archive.Close(); err != nil {
		archiveFile.Close()
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}

	for _, source := range []string{markdownPath, zipPath} {
		accepted := request(t, handler, "POST", "/api/v1/skills/import", map[string]any{"path": source}, "desktop-test")
		if accepted.Code != http.StatusAccepted {
			t.Fatalf("import %s: %d %s", source, accepted.Code, accepted.Body.String())
		}
		var job struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(accepted.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if job.ID == "" {
			t.Fatalf("import %s returned an empty job id", source)
		}

		var completed bool
		for attempt := 0; attempt < 100; attempt++ {
			jobsResponse := request(t, handler, "GET", "/api/v1/jobs", nil, "desktop-test")
			if jobsResponse.Code != http.StatusOK {
				t.Fatalf("jobs for %s: %d %s", source, jobsResponse.Code, jobsResponse.Body.String())
			}
			var jobs []struct {
				ID      string `json:"id"`
				Status  string `json:"status"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(jobsResponse.Body.Bytes(), &jobs); err != nil {
				t.Fatal(err)
			}
			for _, item := range jobs {
				if item.ID != job.ID {
					continue
				}
				switch item.Status {
				case "completed":
					completed = true
				case "failed":
					t.Fatalf("import %s failed: %s", source, item.Message)
				}
			}
			if completed {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !completed {
			t.Fatalf("import %s did not complete", source)
		}
	}

	listed := request(t, handler, "GET", "/api/v1/skills", nil, "desktop-test")
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte("http-markdown-skill")) || !bytes.Contains(listed.Body.Bytes(), []byte("http-zip-skill")) {
		t.Fatalf("skills after HTTP imports: %d %s", listed.Code, listed.Body.String())
	}
}

func TestSavedSearchLifecycle(t *testing.T) {
	_, handler := testServer(t)
	created := request(t, handler, "POST", "/api/v1/saved-searches", map[string]any{"name": "Portable notes", "query": "portable knowledge", "libraryIds": []string{"library-1"}}, "desktop-test")
	if created.Code != http.StatusCreated || !bytes.Contains(created.Body.Bytes(), []byte("portable knowledge")) {
		t.Fatalf("create saved search: %d %s", created.Code, created.Body.String())
	}
	var item struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	listed := request(t, handler, "GET", "/api/v1/saved-searches", nil, "desktop-test")
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte("Portable notes")) {
		t.Fatalf("list saved searches: %d %s", listed.Code, listed.Body.String())
	}
	deleted := request(t, handler, "DELETE", "/api/v1/saved-searches/"+item.ID, nil, "desktop-test")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete saved search: %d %s", deleted.Code, deleted.Body.String())
	}
}

package httpapi

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOCRPluginLifecycleThroughWorker(t *testing.T) {
	_, handler := testServerWithWorker(t)
	path := filepath.Join(t.TempDir(), "ocr.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entries := []map[string]string{}
	for _, name := range []string{"det.onnx", "rec.onnx", "cls.onnx", "dict.txt"} {
		data := []byte("fake resource for lifecycle test")
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write(data); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, map[string]string{"name": name, "sha256": fmt.Sprintf("%x", sha256.Sum256(data))})
	}
	manifest, _ := json.Marshal(map[string]any{"schema": "kah-ocr-resource/v1", "version": "test", "files": entries})
	writer, err := archive.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if response := request(t, handler, "DELETE", "/api/v1/plugins/ocr", nil, ""); response.Code == 200 {
		t.Fatal("unauthenticated uninstall")
	}
	for _, step := range []struct {
		method, path       string
		body               any
		installed, enabled bool
	}{
		{"POST", "/api/v1/plugins/ocr/install", map[string]string{"path": path}, true, true},
		{"PATCH", "/api/v1/plugins/ocr", map[string]bool{"enabled": false}, true, false},
		{"GET", "/api/v1/plugins/ocr", nil, true, false},
		{"PATCH", "/api/v1/plugins/ocr", map[string]bool{"enabled": true}, true, true},
		{"DELETE", "/api/v1/plugins/ocr", nil, false, false},
	} {
		response := request(t, handler, step.method, step.path, step.body, "desktop-test")
		var result struct{ Installed, Enabled bool }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Installed != step.installed || result.Enabled != step.enabled {
			t.Fatalf("%s: %d %s", step.method, response.Code, response.Body.String())
		}
	}
}

// Opt-in real-model gate. The normal suite must not download models or require
// a developer's private data. Generate the fixture with verify-real-ocr.py.
func TestRealOCRPluginImportCreatesKAHReference(t *testing.T) {
	packagePath, pdfPath := os.Getenv("KAH_TEST_REAL_OCR_PACKAGE"), os.Getenv("KAH_TEST_REAL_OCR_PDF")
	if packagePath == "" || pdfPath == "" {
		t.Skip("set real OCR package and generated scanned PDF paths")
	}
	server, handler := testServerWithWorker(t)
	installed := request(t, handler, "POST", "/api/v1/plugins/ocr/install", map[string]string{"path": packagePath}, "desktop-test")
	if installed.Code != 200 {
		t.Fatal(installed.Body.String())
	}
	libraryID := createStage4Library(t, handler, "真实 OCR 无 Agent 转换")
	response := request(t, handler, "POST", "/api/v1/imports/files", map[string]any{"libraryId": libraryID, "paths": []string{pdfPath}}, "desktop-test")
	if response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	var jobs []model.Job
	if err := json.Unmarshal(response.Body.Bytes(), &jobs); err != nil || len(jobs) != 1 {
		t.Fatalf("import jobs: %v", err)
	}
	complete := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		current, err := server.Store.ListJobs(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range current {
			if job.ID == jobs[0].ID {
				if job.Status == "failed" {
					t.Fatal(job.Message)
				}
				complete = job.Status == "completed"
			}
		}
		if complete {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !complete {
		t.Fatal("real OCR import timed out")
	}
	submissions, err := server.Store.ListKAHSubmissions(context.Background(), libraryID)
	if err != nil || len(submissions) != 1 {
		t.Fatalf("reference draft missing: %v %v", submissions, err)
	}
	encoded, _ := json.Marshal(submissions)
	if !strings.Contains(string(encoded), "reference") {
		t.Fatal("not a reference KAH draft")
	}
	removed := request(t, handler, "DELETE", "/api/v1/plugins/ocr", nil, "desktop-test")
	if removed.Code != 200 {
		t.Fatal(removed.Body.String())
	}
	preserved, err := server.Store.ListKAHSubmissions(context.Background(), libraryID)
	if err != nil || len(preserved) != 1 {
		t.Fatal("uninstall removed knowledge")
	}
}

// Opt-in end-to-end layout gate. It proves that the Worker layout metadata is
// retained by the API document endpoint and that the non-Agent KAH draft still
// pins the imported document as its immutable source.
func TestRealComplexOCRImportRetainsLayoutMetadata(t *testing.T) {
	packagePath, pdfPath := os.Getenv("KAH_TEST_REAL_OCR_PACKAGE"), os.Getenv("KAH_TEST_COMPLEX_OCR_PDF")
	if packagePath == "" || pdfPath == "" {
		t.Skip("set real OCR package and generated complex PDF paths")
	}
	server, handler := testServerWithWorker(t)
	installed := request(t, handler, "POST", "/api/v1/plugins/ocr/install", map[string]string{"path": packagePath}, "desktop-test")
	if installed.Code != 200 {
		t.Fatal(installed.Body.String())
	}
	libraryID := createStage4Library(t, handler, "真实复杂版式 OCR")
	response := request(t, handler, "POST", "/api/v1/imports/files", map[string]any{"libraryId": libraryID, "paths": []string{pdfPath}}, "desktop-test")
	if response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	var jobs []model.Job
	if err := json.Unmarshal(response.Body.Bytes(), &jobs); err != nil || len(jobs) != 1 {
		t.Fatalf("import jobs: %v", err)
	}
	complete := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		current, err := server.Store.ListJobs(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range current {
			if job.ID == jobs[0].ID {
				if job.Status == "failed" {
					t.Fatal(job.Message)
				}
				complete = job.Status == "completed"
			}
		}
		if complete {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !complete {
		t.Fatal("real complex OCR import timed out")
	}

	documentsResponse := request(t, handler, "GET", "/api/v1/documents?libraryId="+libraryID, nil, "desktop-test")
	if documentsResponse.Code != 200 {
		t.Fatal(documentsResponse.Body.String())
	}
	var documents []model.Document
	if err := json.Unmarshal(documentsResponse.Body.Bytes(), &documents); err != nil || len(documents) != 1 {
		t.Fatalf("documents: %v %#v", err, documents)
	}
	detailResponse := request(t, handler, "GET", "/api/v1/documents/"+documents[0].ID, nil, "desktop-test")
	if detailResponse.Code != 200 {
		t.Fatal(detailResponse.Body.String())
	}
	var detail model.DocumentDetail
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	var pageOne, pageTwo map[string]any
	for _, chunk := range detail.Preview {
		if page, ok := chunk.Location["page"].(float64); ok {
			switch int(page) {
			case 1:
				pageOne = chunk.Location
			case 2:
				pageTwo = chunk.Location
			}
		}
	}
	if layout, ok := pageOne["ocrLayout"].(map[string]any); !ok || layout["mode"] != "column-major" || layout["columnCount"] != float64(2) {
		t.Fatalf("page one column layout not retained: %#v", pageOne)
	}
	layout, ok := pageTwo["ocrLayout"].(map[string]any)
	if !ok {
		t.Fatalf("page two layout not retained: %#v", pageTwo)
	}
	tables, ok := layout["tables"].([]any)
	if !ok || len(tables) != 1 {
		t.Fatalf("page two table metadata not retained: %#v", layout)
	}
	table, ok := tables[0].(map[string]any)
	if !ok || fmt.Sprint(table["headers"]) != "[编号 类型 页数 状态]" || len(table["rows"].([]any)) != 4 {
		t.Fatalf("page two table structure = %#v", table)
	}

	submissions, err := server.Store.ListKAHSubmissions(context.Background(), libraryID)
	if err != nil || len(submissions) != 1 || submissions[0].ReviewStatus != "pending_review" {
		t.Fatalf("KAH draft = %#v, error = %v", submissions, err)
	}
	var draft strings.Builder
	for _, section := range submissions[0].Validation.Normalized.Sections {
		draft.WriteString(section.Content)
	}
	for _, expected := range []string{"#### 第2页", "按 OCR 坐标恢复的表格", "| 编号 | 类型 | 页数 | 状态 |", "| T04 | 低对比度 | 5 | 通过 |"} {
		if !strings.Contains(draft.String(), expected) {
			t.Fatalf("structured KAH draft lost %q: %s", expected, draft.String())
		}
	}
	source := submissions[0].Validation.Normalized.Sources[0]
	if source.Resource != "kah://document/"+documents[0].ID || source.Snapshot.ContentHash != documents[0].ContentHash {
		t.Fatalf("KAH source is not pinned to the retained OCR document: %#v", source)
	}
}

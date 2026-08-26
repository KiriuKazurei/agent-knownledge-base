package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestGetDocumentRepairsLegacyChineseChunks(t *testing.T) {
	server, handler := testServer(t)
	libraryID := createStage4Library(t, handler, "中文文档")
	source := filepath.Join(t.TempDir(), "README.zh.md")
	content := "# 中文标题\n\n正文中文内容。\n"
	if err := os.WriteFile(source, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	doc, err := server.Store.CreatePendingDocument(context.Background(), libraryID, "README.zh.md", "text/markdown", source, "", "", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Store.ReplaceChunks(context.Background(), doc.ID, []model.Chunk{{ID: "legacy", DocumentID: doc.ID, Text: "# ����标题\n\n����中文内容。", Location: map[string]any{"ordinal": 0}, ContentHash: "legacy-hash"}}); err != nil {
		t.Fatal(err)
	}

	response := request(t, handler, http.MethodGet, "/api/v1/documents/"+doc.ID, nil, "desktop-test")
	if response.Code != http.StatusOK {
		t.Fatalf("get document: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "中文标题") || !strings.Contains(body, "正文中文内容") {
		t.Fatalf("document preview was not repaired: %s", body)
	}
	if strings.Contains(body, "�") {
		t.Fatalf("document preview still contains replacement characters: %s", body)
	}
	repaired, err := server.Store.GetDocument(context.Background(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired.Preview) != 1 || repaired.Preview[0].Text != strings.TrimSpace(content) {
		t.Fatalf("stored chunks were not repaired: %#v", repaired.Preview)
	}
}

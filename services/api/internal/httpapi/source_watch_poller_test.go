package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestSourceWatchPollerQueuesChangedFile(t *testing.T) {
	server, handler := testServerWithWorker(t)
	libraryID := createStage4Library(t, handler, "来源自动监视")
	root := t.TempDir()
	source := filepath.Join(root, "polling.md")
	if err := os.WriteFile(source, []byte("# 初始\n\n轮询前内容\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	watch, err := server.Store.CreateSourceWatch(context.Background(), libraryID, root, true)
	if err != nil {
		t.Fatal(err)
	}
	initialJob, err := server.Store.CreateJob(context.Background(), "source_scan", map[string]any{"watchId": watch.ID})
	if err != nil {
		t.Fatal(err)
	}
	server.runSourceWatchScan(initialJob.ID, watch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.runSourceWatchPoller(ctx, 20*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	if err := os.WriteFile(source, []byte("# 已修改\n\n轮询后内容已进入索引。\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response := request(t, handler, http.MethodGet, "/api/v1/documents?libraryId="+libraryID, nil, "desktop-test")
		if response.Code != http.StatusOK {
			t.Fatalf("list documents: %d %s", response.Code, response.Body.String())
		}
		var documents []model.Document
		if err := json.Unmarshal(response.Body.Bytes(), &documents); err != nil {
			t.Fatal(err)
		}
		if len(documents) == 1 && documents[0].Status == "ready" {
			detail := request(t, handler, http.MethodGet, "/api/v1/documents/"+documents[0].ID, nil, "desktop-test")
			if strings.Contains(detail.Body.String(), "轮询后内容") && !strings.Contains(detail.Body.String(), "轮询前内容") {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("source watch poller did not import the changed file")
}

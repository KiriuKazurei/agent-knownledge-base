package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"
)

func TestRuntimeShutdownDesktopOnlyAndQuiescent(t *testing.T) {
	s, handler := testServer(t)
	stopped := make(chan struct{}, 1)
	s.SetRuntimeShutdown(func() { stopped <- struct{}{} })
	if got := request(t, handler, "POST", "/api/v1/runtime/shutdown", nil, "").Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", got)
	}
	sum := sha256.Sum256([]byte("agent-test"))
	_, err := s.Store.CreateToken(context.Background(), "agent", hex.EncodeToString(sum[:]), []string{"mcp_manage"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := request(t, handler, "POST", "/api/v1/runtime/shutdown", nil, "agent-test").Code; got != http.StatusForbidden {
		t.Fatalf("agent: %d", got)
	}
	if !s.beginRuntimeOperation() {
		t.Fatal("admission closed")
	}
	if got := request(t, handler, "POST", "/api/v1/runtime/shutdown", nil, "desktop-test").Code; got != http.StatusConflict {
		t.Fatalf("active request: %d", got)
	}
	s.endRuntimeOperation()
	job, err := s.Store.CreateJob(context.Background(), "file_import", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if got := request(t, handler, "POST", "/api/v1/runtime/shutdown", nil, "desktop-test").Code; got != http.StatusConflict {
		t.Fatalf("queued job: %d", got)
	}
	if err := s.Store.UpdateJob(context.Background(), job.ID, "completed", 1, "done"); err != nil {
		t.Fatal(err)
	}
	s.retrievalRunning["library"] = true
	if got := request(t, handler, "POST", "/api/v1/runtime/shutdown", nil, "desktop-test").Code; got != http.StatusConflict {
		t.Fatalf("rebuild: %d", got)
	}
	delete(s.retrievalRunning, "library")
	if got := request(t, handler, "POST", "/api/v1/runtime/shutdown", nil, "desktop-test").Code; got != http.StatusAccepted {
		t.Fatalf("idle: %d", got)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown not called")
	}
	if s.beginRuntimeOperation() {
		t.Fatal("scheduler admitted work after shutdown")
	}
	if got := request(t, handler, "POST", "/api/v1/libraries", map[string]string{"name": "blocked"}, "desktop-test").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("new work: %d", got)
	}
	if got := request(t, handler, "POST", "/api/v1/runtime/shutdown", nil, "desktop-test").Code; got != http.StatusAccepted {
		t.Fatalf("idempotent: %d", got)
	}
	select {
	case <-stopped:
		t.Fatal("shutdown invoked twice")
	default:
	}
}

func TestSkillMappingDetailRouteExists(t *testing.T) {
	_, handler := testServer(t)
	response := request(t, handler, "GET", "/api/v1/skill-mapping-targets/missing", nil, "desktop-test")
	if response.Code != http.StatusNotFound || response.Body.String() == "404 page not found" {
		t.Fatalf("detail handler missing: %d %s", response.Code, response.Body.String())
	}
}

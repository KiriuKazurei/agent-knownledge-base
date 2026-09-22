package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestEmbeddingModelRoutesUseDesktopScopeAndFixedCatalog(t *testing.T) {
	_, handler := testServerWithWorker(t)
	if response := request(t, handler, http.MethodGet, "/api/v1/embedding-models", nil, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated embedding list: %d %s", response.Code, response.Body.String())
	}
	listed := request(t, handler, http.MethodGet, "/api/v1/embedding-models", nil, "desktop-test")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "BAAI/bge-small-zh-v1.5") {
		t.Fatalf("embedding catalog: %d %s", listed.Code, listed.Body.String())
	}
	if response := request(t, handler, http.MethodGet, "/api/v1/embedding-models/health", nil, "desktop-test"); response.Code != http.StatusBadRequest {
		t.Fatalf("health without model id: %d %s", response.Code, response.Body.String())
	}
	// The catalog advertises the fixed, hash-verified online resource.  Keep
	// this API test network-free; the real download path is covered by the
	// worker validation evidence.
	if !strings.Contains(listed.Body.String(), `"onlineInstall":true`) {
		t.Fatalf("fixed BGE catalog is not installable: %s", listed.Body.String())
	}
}

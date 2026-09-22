package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Embedding model management is desktop-only. The model package is handed to
// the worker, which performs the archive, manifest and hash checks in the same
// process that owns the local model directory.
func (s *Server) embeddingModels(c *gin.Context) {
	if s.Worker == nil {
		s.problem(c, http.StatusServiceUnavailable, "worker_unavailable", "Document worker is unavailable", true)
		return
	}

	method := "embedding_models"
	params := map[string]any{}
	switch {
	case c.Request.Method == http.MethodGet && strings.HasSuffix(c.FullPath(), "/health"):
		modelID := strings.TrimSpace(c.Query("modelId"))
		if modelID == "" {
			s.problem(c, http.StatusBadRequest, "invalid_model_id", "modelId is required", false)
			return
		}
		method = "model_health"
		params["modelId"] = modelID
	case c.Request.Method == http.MethodGet:
		method = "embedding_models"
	case c.Request.Method == http.MethodPost && strings.HasSuffix(c.FullPath(), "/install"):
		var input struct {
			Path string `json:"path"`
		}
		if !bind(c, &input) {
			return
		}
		if strings.TrimSpace(input.Path) == "" {
			s.problem(c, http.StatusBadRequest, "invalid_path", "Select an embedding model ZIP", false)
			return
		}
		method = "embedding_install"
		params["path"] = input.Path
	case c.Request.Method == http.MethodPost && strings.HasSuffix(c.FullPath(), "/install-online"):
		var input struct {
			ModelID string `json:"modelId"`
		}
		if !bind(c, &input) {
			return
		}
		if strings.TrimSpace(input.ModelID) == "" {
			s.problem(c, http.StatusBadRequest, "invalid_model_id", "modelId is required", false)
			return
		}
		method = "embedding_install_online"
		params["modelId"] = input.ModelID
	case c.Request.Method == http.MethodPatch:
		var input struct {
			ModelID string `json:"modelId"`
			Enabled *bool  `json:"enabled"`
		}
		if !bind(c, &input) {
			return
		}
		if strings.TrimSpace(input.ModelID) == "" || input.Enabled == nil {
			s.problem(c, http.StatusBadRequest, "invalid_model_action", "modelId and enabled are required", false)
			return
		}
		method = "embedding_enable"
		params["modelId"] = input.ModelID
		params["enabled"] = *input.Enabled
	case c.Request.Method == http.MethodDelete:
		var input struct {
			ModelID string `json:"modelId"`
		}
		if !bind(c, &input) {
			return
		}
		if strings.TrimSpace(input.ModelID) == "" {
			s.problem(c, http.StatusBadRequest, "invalid_model_id", "modelId is required", false)
			return
		}
		method = "embedding_uninstall"
		params["modelId"] = input.ModelID
	default:
		s.problem(c, http.StatusMethodNotAllowed, "method_not_allowed", "Unsupported embedding model operation", false)
		return
	}

	result := map[string]any{}
	if err := s.Worker.Call(operationContext(c), method, params, &result); err != nil {
		status := http.StatusBadRequest
		if strings.HasPrefix(method, "model_health") {
			status = http.StatusServiceUnavailable
		}
		s.problem(c, status, "embedding_model_failed", err.Error(), status >= http.StatusServiceUnavailable)
		return
	}
	c.JSON(http.StatusOK, result)
}

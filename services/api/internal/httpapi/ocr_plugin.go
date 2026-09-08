package httpapi

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
)

// These routes inherit desktop administrator authentication, never Agent scopes.
func (s *Server) ocrPlugin(c *gin.Context) {
	if s.Worker == nil {
		s.problem(c, 503, "worker_unavailable", "Document worker is unavailable", true)
		return
	}
	method := "ocr_plugin_status"
	params := map[string]any{}
	switch c.Request.Method {
	case http.MethodPost:
		var input struct {
			Path string `json:"path"`
		}
		if !bind(c, &input) {
			return
		}
		if strings.TrimSpace(input.Path) == "" {
			s.problem(c, 400, "invalid_path", "Select an OCR plugin ZIP", false)
			return
		}
		method = "ocr_plugin_install"
		params["path"] = input.Path
	case http.MethodPatch:
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if !bind(c, &input) {
			return
		}
		if input.Enabled == nil {
			s.problem(c, 400, "invalid_enabled", "enabled is required", false)
			return
		}
		method = "ocr_plugin_enabled"
		params["enabled"] = *input.Enabled
	case http.MethodDelete:
		method = "ocr_plugin_uninstall"
	}
	result := map[string]any{}
	if err := s.Worker.Call(operationContext(c), method, params, &result); err != nil {
		s.problem(c, 400, "ocr_plugin_failed", err.Error(), true)
		return
	}
	c.JSON(200, result)
}

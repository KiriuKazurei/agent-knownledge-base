package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/gin-gonic/gin"
)

func (s *Server) desktopKnowledgeSearch(c *gin.Context) {
	var request model.KnowledgeSearchRequest
	if !bind(c, &request) {
		return
	}
	items, err := s.Store.SearchKnowledge(operationContext(c), request)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) desktopKnowledgeGet(c *gin.Context) {
	uri := strings.TrimSpace(c.Query("uri"))
	if uri == "" {
		s.problem(c, http.StatusBadRequest, "invalid_request", "uri is required", false)
		return
	}
	item, err := s.Store.GetKnowledge(operationContext(c), uri, true)
	if errors.Is(err, storage.ErrKnowledgeNotFound) || errors.Is(err, storage.ErrRevisionNotFound) {
		s.problem(c, http.StatusNotFound, "knowledge_not_found", "Knowledge revision not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusBadRequest, "knowledge_read_failed", err.Error(), false)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) listKAHSubmissions(c *gin.Context) {
	items, err := s.Store.ListKAHSubmissions(operationContext(c), c.Query("libraryId"))
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) getKAHSubmission(c *gin.Context) {
	item, err := s.Store.GetKAHSubmission(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "submission_not_found", "Knowledge submission not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) approveKAHSubmission(c *gin.Context) {
	var input struct {
		Reason string `json:"reason"`
	}
	if c.Request.ContentLength != 0 && !bind(c, &input) {
		return
	}
	item, err := s.Store.ReviewKAHSubmission(operationContext(c), c.Param("id"), actorName(c), "approve", input.Reason)
	if err != nil {
		s.problem(c, http.StatusBadRequest, "submission_review_failed", err.Error(), false)
		return
	}
	published, err := s.Store.PublishKAHSubmission(operationContext(c), item.ID)
	if err != nil {
		s.problem(c, http.StatusConflict, "submission_publish_failed", err.Error(), false)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "kah_knowledge_published", published.URI, map[string]any{"revision": published.Revision, "submissionId": item.ID})
	c.JSON(http.StatusOK, published)
}

func (s *Server) rejectKAHSubmission(c *gin.Context) {
	var input struct {
		Reason string `json:"reason"`
	}
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.ReviewKAHSubmission(operationContext(c), c.Param("id"), actorName(c), "reject", input.Reason)
	if err != nil {
		s.problem(c, http.StatusBadRequest, "submission_review_failed", err.Error(), false)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "kah_knowledge_rejected", item.KnowledgeURI, map[string]any{"submissionId": item.ID})
	c.JSON(http.StatusOK, item)
}

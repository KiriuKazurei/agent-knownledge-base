package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/gin-gonic/gin"
)

func graphIntegerQuery(c *gin.Context, name string, fallback int) (int, bool) {
	raw := c.Query(name)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return value, true
}

func (s *Server) getRelationshipGraph(c *gin.Context) {
	libraryID := c.Query("libraryId")
	if libraryID == "" {
		s.problem(c, http.StatusBadRequest, "graph_library_required", "libraryId is required", false)
		return
	}
	depth, ok := graphIntegerQuery(c, "depth", 1)
	if !ok || depth < 1 {
		s.problem(c, http.StatusBadRequest, "graph_depth_invalid", "depth must be a positive integer", false)
		return
	}
	limit, ok := graphIntegerQuery(c, "limit", 250)
	if !ok || limit < 1 {
		s.problem(c, http.StatusBadRequest, "graph_limit_invalid", "limit must be a positive integer", false)
		return
	}
	snapshot, err := s.Store.BuildGraphSnapshot(operationContext(c), libraryID, c.Query("workspaceId"), c.Query("centerNodeId"), depth, limit)
	if err != nil {
		if errors.Is(err, storage.ErrGraphWorkspaceNotFound) || errors.Is(err, storage.ErrGraphNodeNotFound) {
			s.problem(c, http.StatusNotFound, "graph_scope_not_found", err.Error(), false)
		} else {
			s.problem(c, http.StatusBadRequest, "graph_read_failed", err.Error(), false)
		}
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func (s *Server) createGraphWorkspace(c *gin.Context) {
	var input struct {
		LibraryID string `json:"libraryId" binding:"required"`
		Name      string `json:"name"`
	}
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.CreateGraphWorkspace(operationContext(c), input.LibraryID, input.Name)
	if err != nil {
		s.problem(c, http.StatusBadRequest, "graph_workspace_create_failed", err.Error(), false)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "graph_workspace_created", item.ID, map[string]any{"libraryId": item.LibraryID, "name": item.Name})
	c.JSON(http.StatusCreated, item)
}

func (s *Server) updateGraphWorkspace(c *gin.Context) {
	var input struct {
		Name     *string         `json:"name"`
		Filters  *map[string]any `json:"filters"`
		Viewport *map[string]any `json:"viewport"`
	}
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.UpdateGraphWorkspace(operationContext(c), c.Param("id"), input.Name, input.Filters, input.Viewport)
	if err != nil {
		if errors.Is(err, storage.ErrGraphWorkspaceNotFound) {
			s.problem(c, http.StatusNotFound, "graph_workspace_not_found", err.Error(), false)
		} else {
			s.problem(c, http.StatusBadRequest, "graph_workspace_update_failed", err.Error(), false)
		}
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "graph_workspace_updated", item.ID, nil)
	c.JSON(http.StatusOK, item)
}

func (s *Server) saveGraphPositions(c *gin.Context) {
	var input struct {
		Positions []model.GraphPositionUpdate `json:"positions" binding:"required,max=300"`
	}
	if !bind(c, &input) {
		return
	}
	positions, err := s.Store.SaveGraphPositions(operationContext(c), c.Param("id"), input.Positions)
	if err != nil {
		if errors.Is(err, storage.ErrGraphWorkspaceNotFound) || errors.Is(err, storage.ErrGraphNodeNotFound) {
			s.problem(c, http.StatusNotFound, "graph_position_target_not_found", err.Error(), false)
		} else {
			s.problem(c, http.StatusBadRequest, "graph_position_save_failed", err.Error(), false)
		}
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "graph_positions_saved", c.Param("id"), map[string]any{"count": len(positions)})
	c.JSON(http.StatusOK, gin.H{"positions": positions})
}

func (s *Server) autoLayoutGraph(c *gin.Context) {
	var input struct {
		LibraryID string `json:"libraryId" binding:"required"`
		Overwrite bool   `json:"overwrite"`
	}
	if !bind(c, &input) {
		return
	}
	positions, err := s.Store.AutoLayoutGraph(operationContext(c), input.LibraryID, c.Param("id"), input.Overwrite)
	if err != nil {
		if errors.Is(err, storage.ErrGraphWorkspaceNotFound) || errors.Is(err, storage.ErrGraphNodeNotFound) {
			s.problem(c, http.StatusNotFound, "graph_layout_scope_not_found", err.Error(), false)
		} else {
			s.problem(c, http.StatusBadRequest, "graph_layout_failed", err.Error(), false)
		}
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "graph_auto_layout", c.Param("id"), map[string]any{"count": len(positions), "overwrite": input.Overwrite})
	c.JSON(http.StatusOK, gin.H{"positions": positions})
}

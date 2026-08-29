package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/gin-gonic/gin"
)

func (s *Server) listSkillMappingTargets(c *gin.Context) {
	items, err := s.Store.ListSkillMappingTargets(operationContext(c))
	if err != nil {
		s.writeSkillMappingError(c, err)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) createSkillMappingTarget(c *gin.Context) {
	var input model.CreateSkillMappingTargetRequest
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.CreateSkillMappingTarget(operationContext(c), input)
	if err != nil {
		_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_mapping_target_create_failed", input.DirectoryPath, skillMappingAuditDetails(c, map[string]any{"name": input.Name, "kind": input.Kind, "skillIds": input.SkillIDs, "error": skillMappingErrorCode(err)}))
		s.writeSkillMappingError(c, err)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_mapping_target_created", item.ID, skillMappingAuditDetails(c, map[string]any{"directoryPath": item.DirectoryPath, "kind": item.Kind, "skillIds": input.SkillIDs}))
	c.JSON(http.StatusCreated, item)
}

func (s *Server) updateSkillMappingTarget(c *gin.Context) {
	var input model.UpdateSkillMappingTargetRequest
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.UpdateSkillMappingTarget(operationContext(c), c.Param("id"), input)
	if err != nil {
		s.writeSkillMappingError(c, err)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_mapping_target_updated", item.ID, skillMappingAuditDetails(c, map[string]any{"name": item.Name, "kind": item.Kind}))
	c.JSON(http.StatusOK, item)
}

func (s *Server) addSkillMappings(c *gin.Context) {
	var input model.SkillMappingIDsRequest
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.AddSkillMappings(operationContext(c), c.Param("id"), input.SkillIDs)
	if err != nil {
		s.writeSkillMappingError(c, err)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_mappings_added", item.ID, skillMappingAuditDetails(c, map[string]any{"skillIds": input.SkillIDs}))
	c.JSON(http.StatusOK, item)
}

func (s *Server) verifySkillMappingTarget(c *gin.Context) {
	item, err := s.Store.VerifySkillMappingTarget(operationContext(c), c.Param("id"))
	if err != nil {
		s.writeSkillMappingError(c, err)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_mapping_target_verified", item.ID, skillMappingAuditDetails(c, map[string]any{"status": item.Status}))
	c.JSON(http.StatusOK, item)
}

func (s *Server) repairSkillMapping(c *gin.Context) {
	item, err := s.Store.RepairSkillMapping(operationContext(c), c.Param("id"), c.Param("skillId"))
	if err != nil {
		s.writeSkillMappingError(c, err)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_mapping_repaired", item.ID, skillMappingAuditDetails(c, map[string]any{"skillId": c.Param("skillId")}))
	c.JSON(http.StatusOK, item)
}

func (s *Server) removeSkillMapping(c *gin.Context) {
	item, err := s.Store.RemoveSkillMapping(operationContext(c), c.Param("id"), c.Param("skillId"))
	if err != nil {
		s.writeSkillMappingError(c, err)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_mapping_removed", item.ID, skillMappingAuditDetails(c, map[string]any{"skillId": c.Param("skillId")}))
	c.Status(http.StatusNoContent)
}

func (s *Server) forgetSkillMapping(c *gin.Context) {
	item, err := s.Store.ForgetSkillMapping(operationContext(c), c.Param("id"), c.Param("skillId"))
	if err != nil {
		s.writeSkillMappingError(c, err)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_mapping_forgotten", item.ID, skillMappingAuditDetails(c, map[string]any{"skillId": c.Param("skillId")}))
	c.Status(http.StatusNoContent)
}

func (s *Server) deleteSkillMappingTarget(c *gin.Context) {
	if err := s.Store.DeleteSkillMappingTarget(operationContext(c), c.Param("id")); err != nil {
		s.writeSkillMappingError(c, err)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_mapping_target_deleted", c.Param("id"), skillMappingAuditDetails(c, nil))
	c.Status(http.StatusNoContent)
}

func skillMappingAuditDetails(c *gin.Context, details map[string]any) map[string]any {
	if details == nil {
		details = map[string]any{}
	}
	details["requestId"] = c.GetString("requestID")
	return details
}

func skillMappingErrorCode(err error) string {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "skill_mapping_target_not_found"
	case errors.Is(err, storage.ErrSkillMappingSkillNotFound):
		return "skill_mapping_skill_not_found"
	case errors.Is(err, storage.ErrSkillMappingPathNotAbsolute):
		return "skill_mapping_path_not_absolute"
	case errors.Is(err, storage.ErrSkillMappingSourceNested):
		return "skill_mapping_source_nested"
	case errors.Is(err, storage.ErrSkillMappingConflict):
		return "skill_mapping_conflict"
	case errors.Is(err, storage.ErrSkillMappingPermissionRequired):
		return "skill_mapping_permission_required"
	case errors.Is(err, storage.ErrSkillMappingLinkInvalid):
		return "skill_mapping_link_invalid"
	case errors.Is(err, storage.ErrSkillMapped):
		return "skill_mapping_skill_in_use"
	case errors.Is(err, storage.ErrSkillMappingNotFound):
		return "skill_mapping_not_found"
	case errors.Is(err, storage.ErrSkillMappingTargetInvalid), errors.Is(err, storage.ErrSkillMappingSourceInvalid):
		return "skill_mapping_target_invalid"
	default:
		return "skill_mapping_failed"
	}
}

func (s *Server) writeSkillMappingError(c *gin.Context, err error) {
	code := skillMappingErrorCode(err)
	status := http.StatusInternalServerError
	retryable := true
	switch {
	case errors.Is(err, sql.ErrNoRows):
		status, retryable = http.StatusNotFound, false
	case errors.Is(err, storage.ErrSkillMappingSkillNotFound), errors.Is(err, storage.ErrSkillMappingNotFound):
		status, retryable = http.StatusNotFound, false
	case errors.Is(err, storage.ErrSkillMappingPathNotAbsolute), errors.Is(err, storage.ErrSkillMappingTargetInvalid), errors.Is(err, storage.ErrSkillMappingSourceNested), errors.Is(err, storage.ErrSkillMappingLinkInvalid), errors.Is(err, storage.ErrSkillMappingSourceInvalid):
		status, retryable = http.StatusBadRequest, false
	case errors.Is(err, storage.ErrSkillMappingConflict), errors.Is(err, storage.ErrSkillMapped):
		status, retryable = http.StatusConflict, false
	case errors.Is(err, storage.ErrSkillMappingPermissionRequired):
		status, retryable = http.StatusForbidden, false
	}
	s.problem(c, status, code, err.Error(), retryable)
}

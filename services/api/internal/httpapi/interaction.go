package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/gin-gonic/gin"
)

func (s *Server) listTrash(c *gin.Context) {
	items, err := s.Store.ListTrash(operationContext(c))
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "trash_list_failed", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) restoreTrash(c *gin.Context) {
	kind, id := c.Param("kind"), c.Param("id")
	if err := s.Store.RestoreTrash(operationContext(c), kind, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "trash_item_not_found", "Trash item not found or its parent library is still in Trash", false)
		} else {
			s.problem(c, http.StatusBadRequest, "trash_restore_failed", err.Error(), false)
		}
		return
	}
	if kind == "document" {
		if job, err := s.Store.CreateJob(operationContext(c), "document_reindex", map[string]any{"documentId": id}); err == nil {
			go s.runDocumentReindex(job.ID, id)
		}
	} else if kind == "library" && s.Worker != nil && s.Worker.State() == "ok" {
		if job, err := s.Store.CreateJob(operationContext(c), "index_rebuild", map[string]any{"libraryId": id}); err == nil {
			go s.runIndexRebuild(job.ID, id)
		}
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "trash_restored", c.Param("kind")+":"+c.Param("id"), nil)
	c.Status(http.StatusNoContent)
}

func (s *Server) purgeTrash(c *gin.Context) {
	if err := s.Store.PurgeTrash(operationContext(c), c.Param("kind"), c.Param("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "trash_item_not_found", "Trash item not found", false)
		} else {
			s.problem(c, http.StatusBadRequest, "trash_purge_failed", err.Error(), false)
		}
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "trash_purged", c.Param("kind")+":"+c.Param("id"), nil)
	c.Status(http.StatusNoContent)
}

func (s *Server) emptyTrash(c *gin.Context) {
	if err := s.Store.PurgeTrash(operationContext(c), "all", ""); err != nil {
		s.problem(c, http.StatusInternalServerError, "trash_empty_failed", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "trash_emptied", "trash", nil)
	c.Status(http.StatusNoContent)
}

func (s *Server) updateFolder(c *gin.Context) {
	var input struct {
		Name     string `json:"name"`
		ParentID string `json:"parentId"`
	}
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.UpdateFolder(operationContext(c), c.Param("id"), input.Name, input.ParentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "folder_not_found", "Folder not found", false)
		} else {
			s.problem(c, http.StatusBadRequest, "folder_update_failed", err.Error(), false)
		}
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) updateSourceWatch(c *gin.Context) {
	var input struct {
		Enabled   *bool `json:"enabled"`
		Recursive *bool `json:"recursive"`
	}
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.UpdateSourceWatch(operationContext(c), c.Param("id"), input.Enabled, input.Recursive)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "source_watch_not_found", "Source watch not found", false)
		} else {
			s.problem(c, http.StatusBadRequest, "source_watch_update_failed", err.Error(), false)
		}
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) batchDocuments(c *gin.Context) {
	var input model.DocumentBatchRequest
	if !bind(c, &input) {
		return
	}
	trashDocuments := []model.DocumentDetail{}
	if input.Action == "trash" {
		for _, id := range input.DocumentIDs {
			detail, detailErr := s.Store.GetDocument(operationContext(c), id)
			if detailErr != nil {
				s.problem(c, http.StatusBadRequest, "document_batch_failed", detailErr.Error(), false)
				return
			}
			trashDocuments = append(trashDocuments, detail)
		}
	}
	result, err := s.Store.BatchDocuments(operationContext(c), input)
	if err != nil {
		s.problem(c, http.StatusBadRequest, "document_batch_failed", err.Error(), false)
		return
	}
	if input.Action == "reindex" {
		for _, id := range result.DocumentIDs {
			job, createErr := s.Store.CreateJob(operationContext(c), "document_reindex", map[string]any{"documentId": id})
			if createErr != nil {
				s.problem(c, http.StatusInternalServerError, "job_create_failed", createErr.Error(), true)
				return
			}
			result.JobIDs = append(result.JobIDs, job.ID)
			go s.runDocumentReindex(job.ID, id)
		}
	}
	if input.Action == "trash" && s.Worker != nil {
		for _, document := range trashDocuments {
			if workerErr := s.Worker.Call(operationContext(c), "index_delete", map[string]any{"libraryId": document.LibraryID, "documentId": document.ID}, nil); workerErr != nil && s.Logger != nil {
				s.Logger.Warn("failed to remove batch-trashed document from index", "documentId", document.ID, "error", workerErr)
			}
		}
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "documents_batch_"+input.Action, strings.Join(result.DocumentIDs, ","), map[string]any{"count": result.UpdatedCount})
	c.JSON(http.StatusOK, result)
}

func (s *Server) reindexDocument(c *gin.Context) {
	input := model.DocumentBatchRequest{DocumentIDs: []string{c.Param("id")}, Action: "reindex"}
	result, err := s.Store.BatchDocuments(operationContext(c), input)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "document_not_found", "Document not found", false)
		} else {
			s.problem(c, http.StatusBadRequest, "document_reindex_failed", err.Error(), false)
		}
		return
	}
	job, err := s.Store.CreateJob(operationContext(c), "document_reindex", map[string]any{"documentId": c.Param("id")})
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "job_create_failed", err.Error(), true)
		return
	}
	result.JobIDs = []string{job.ID}
	go s.runDocumentReindex(job.ID, c.Param("id"))
	c.JSON(http.StatusAccepted, gin.H{"job": job, "result": result})
}

func (s *Server) runDocumentReindex(jobID, documentID string) {
	ctx := context.Background()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.15, "Loading persisted chunks")
	if s.stopIfJobCancellationRequested(ctx, jobID) {
		return
	}
	detail, err := s.Store.GetDocument(ctx, documentID)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if s.stopIfJobCancellationRequested(ctx, jobID) {
		return
	}
	if s.Worker != nil {
		if err := s.Worker.Call(ctx, "index_upsert", map[string]any{"libraryId": detail.LibraryID, "documentId": detail.ID, "chunks": detail.Preview}, nil); err != nil {
			_ = s.Store.FailDocument(ctx, detail.ID, err)
			s.failJob(ctx, jobID, err)
			return
		}
	}
	if s.stopIfJobCancellationRequested(ctx, jobID) {
		return
	}
	if err := s.Store.ReplaceChunks(ctx, detail.ID, detail.Preview); err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if !s.stopIfJobCancellationRequested(ctx, jobID) {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Document index refreshed")
	}
}

func (s *Server) relinkDocument(c *gin.Context) {
	var input struct {
		SourcePath string `json:"sourcePath"`
	}
	if !bind(c, &input) {
		return
	}
	detail, err := s.Store.RelinkDocumentSource(operationContext(c), c.Param("id"), input.SourcePath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "document_not_found", "Document not found", false)
		} else {
			s.problem(c, http.StatusBadRequest, "document_relink_failed", err.Error(), false)
		}
		return
	}
	if err := s.repairDocumentText(operationContext(c), detail); err != nil {
		s.problem(c, http.StatusBadRequest, "document_relink_failed", err.Error(), false)
		return
	}
	updated, err := s.Store.GetDocument(operationContext(c), detail.ID)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (s *Server) updateSavedSearch(c *gin.Context) {
	var input model.SavedSearch
	if !bind(c, &input) {
		return
	}
	input.ID = c.Param("id")
	item, err := s.Store.UpdateSavedSearch(operationContext(c), input)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "saved_search_not_found", "Saved search not found", false)
		} else {
			s.problem(c, http.StatusBadRequest, "saved_search_update_failed", err.Error(), false)
		}
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) cancelJob(c *gin.Context) {
	item, err := s.Store.CancelJob(operationContext(c), c.Param("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "job_not_found", "Job not found", false)
		} else {
			s.problem(c, http.StatusConflict, "job_not_cancellable", err.Error(), false)
		}
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) retryJob(c *gin.Context) {
	item, err := s.Store.CloneJobForRetry(operationContext(c), c.Param("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "job_not_found", "Job not found", false)
		} else {
			s.problem(c, http.StatusConflict, "job_not_retryable", err.Error(), false)
		}
		return
	}
	go func() { _ = s.ResumeQueuedJobs(context.Background()) }()
	c.JSON(http.StatusAccepted, item)
}

func (s *Server) testProvider(c *gin.Context) {
	s.listProviderModels(c)
}

func (s *Server) deleteProvider(c *gin.Context) {
	if err := s.Store.DeleteProvider(operationContext(c), c.Param("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, http.StatusNotFound, "provider_not_found", "Provider not found", false)
		} else {
			s.problem(c, http.StatusConflict, "provider_in_use", err.Error(), false)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) verifyBackup(c *gin.Context) {
	var input struct {
		Path string `json:"path"`
	}
	if !bind(c, &input) {
		return
	}
	manifest, digest, err := s.Store.VerifyBackup(operationContext(c), input.Path)
	if err != nil {
		s.problem(c, http.StatusBadRequest, "backup_invalid", err.Error(), false)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": input.Path, "sha256": digest, "manifest": manifest})
}

func (s *Server) createDesktopKnowledgeSubmission(c *gin.Context) {
	var input struct {
		LibraryID          string                 `json:"libraryId"`
		Mode               string                 `json:"mode"`
		KnowledgeURI       string                 `json:"knowledgeUri"`
		BaseRevision       int                    `json:"baseRevision"`
		ClientSubmissionID string                 `json:"clientSubmissionId"`
		Payload            model.KnowledgePayload `json:"payload"`
	}
	if !bind(c, &input) {
		return
	}
	baseURI := ""
	if input.Mode == "propose_revision" {
		knowledgeID, _, _, err := storage.ParseKnowledgeURI(input.KnowledgeURI)
		if err != nil || input.BaseRevision < 1 {
			s.problem(c, http.StatusBadRequest, "invalid_base_revision", "A valid knowledgeUri and baseRevision are required", false)
			return
		}
		stable, err := s.Store.GetKnowledge(operationContext(c), storage.KnowledgeURI(knowledgeID), false)
		if err != nil {
			s.problem(c, http.StatusNotFound, "knowledge_not_found", err.Error(), false)
			return
		}
		if stable.Revision != input.BaseRevision {
			s.problem(c, http.StatusConflict, "base_revision_stale", fmt.Sprintf("Stable revision changed from r%d to r%d", input.BaseRevision, stable.Revision), false)
			return
		}
		baseURI = storage.KnowledgeURI(knowledgeID) + "?revision=" + fmt.Sprint(input.BaseRevision)
	}
	item, replayed, err := s.Store.CreateKnowledgeDraft(operationContext(c), storage.KnowledgeDraftInput{
		LibraryID: input.LibraryID, TokenID: "", ClientSubmissionID: input.ClientSubmissionID,
		Mode: input.Mode, BaseURI: baseURI, Payload: input.Payload, RequireSources: false,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"problem": gin.H{"code": "knowledge_submission_invalid", "detail": err.Error()}, "validation": item.Validation})
		return
	}
	if replayed {
		c.Header("X-Idempotent-Replay", "true")
	}
	c.JSON(http.StatusCreated, item)
}

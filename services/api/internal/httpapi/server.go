package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/config"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/providers"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/secrets"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/worker"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Server struct {
	Config    config.Config
	Store     *storage.Store
	Worker    *worker.Client
	Providers *providers.Client
	Logger    *slog.Logger
}
type identity struct {
	Desktop bool
	Token   model.AgentToken
}

func actorName(c *gin.Context) string {
	value, _ := c.Get("identity")
	id, _ := value.(identity)
	if id.Desktop {
		return "desktop"
	}
	return id.Token.ID
}

func New(cfg config.Config, store *storage.Store, workerClient *worker.Client, logger *slog.Logger) *Server {
	s := &Server{Config: cfg, Store: store, Worker: workerClient, Providers: providers.New(), Logger: logger}
	if store != nil {
		if _, err := store.EnsureSubmissionFormatter(context.Background()); err != nil && logger != nil {
			logger.Warn("failed to ensure submission formatter Skill", "error", err)
		}
	}
	return s
}

func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), s.requestContext(), cors())
	v1 := router.Group("/api/v1")
	v1.GET("/health", s.health)
	auth := v1.Group("")
	auth.Use(s.authenticate())
	auth.POST("/query", s.requireScope("query"), s.query)
	auth.POST("/query/stream", s.requireScope("query"), s.queryStream)
	auth.POST("/skills/query", s.requireScope("query"), s.querySkills)
	auth.GET("/skills/:id/manifest", s.requireScope("query"), s.skillManifest)
	auth.GET("/skills/:id/files/*path", s.requireScope("query"), s.skillFile)
	auth.POST("/feedback", s.requireScope("feedback"), s.feedback)
	auth.POST("/knowledge-submissions/prepare", s.requireScope("submit"), s.prepareKnowledgeSubmission)
	auth.GET("/knowledge-submissions", s.requireScope("submit"), s.listKnowledgeSubmissions)
	auth.GET("/knowledge-submissions/:id", s.requireScope("submit"), s.getKnowledgeSubmission)
	auth.POST("/knowledge-submissions", s.requireScope("submit"), s.submitKnowledgeSubmission)
	auth.POST("/knowledge-submissions/:id/approve", requireDesktop(), s.approveKnowledgeSubmission)
	auth.POST("/knowledge-submissions/:id/reject", requireDesktop(), s.rejectKnowledgeSubmission)
	auth.POST("/knowledge-submissions/:id/retry-review", requireDesktop(), s.retryKnowledgeSubmissionReview)
	admin := auth.Group("")
	admin.Use(requireDesktop())
	admin.GET("/libraries", s.listLibraries)
	admin.POST("/libraries", s.createLibrary)
	admin.PATCH("/libraries/:id", s.updateLibrary)
	admin.DELETE("/libraries/:id", s.deleteLibrary)
	admin.GET("/documents", s.listDocuments)
	admin.GET("/documents/:id", s.getDocument)
	admin.PATCH("/documents/:id", s.updateDocument)
	admin.GET("/folders", s.listFolders)
	admin.POST("/folders", s.createFolder)
	admin.DELETE("/folders/:id", s.deleteFolder)
	admin.PUT("/documents/:id/folders/:folderId", s.assignDocumentFolder)
	admin.DELETE("/documents/:id/folders/:folderId", s.removeDocumentFolder)
	admin.GET("/sources/watches", s.listSourceWatches)
	admin.POST("/sources/watches", s.createSourceWatch)
	admin.POST("/sources/watches/:id/scan", s.scanSourceWatch)
	admin.DELETE("/sources/watches/:id", s.deleteSourceWatch)
	admin.DELETE("/documents/:id", s.deleteDocument)
	admin.GET("/skills", s.listSkills)
	admin.GET("/skills/:id", s.getSkill)
	admin.POST("/skills/import", s.importSkill)
	admin.PUT("/skills/:id/links", s.updateSkillLinks)
	admin.DELETE("/skills/:id", s.deleteSkill)
	admin.POST("/imports/files", s.importFiles)
	admin.POST("/imports/url", s.importURL)
	admin.GET("/jobs", s.listJobs)
	admin.GET("/saved-searches", s.listSavedSearches)
	admin.POST("/saved-searches", s.createSavedSearch)
	admin.DELETE("/saved-searches/:id", s.deleteSavedSearch)
	admin.GET("/tokens", s.listTokens)
	admin.POST("/tokens", s.createToken)
	admin.DELETE("/tokens/:id", s.revokeToken)
	admin.GET("/providers", s.listProviders)
	admin.POST("/providers", s.saveProvider)
	admin.GET("/providers/:id/models", s.listProviderModels)
	admin.GET("/audit", s.listAudit)
	admin.POST("/backups", s.createBackup)
	admin.POST("/indexes/rebuild", s.rebuildIndex)
	return router
}

func (s *Server) requestContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Set("requestID", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}
func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "null" || origin == "file://" || strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (s *Server) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if token == "" {
			s.problem(c, http.StatusUnauthorized, "auth_required", "Authentication required", false)
			c.Abort()
			return
		}
		if s.Config.DesktopToken != "" && subtleEqual(token, s.Config.DesktopToken) {
			c.Set("identity", identity{Desktop: true})
			c.Next()
			return
		}
		sum := sha256.Sum256([]byte(token))
		agent, err := s.Store.FindToken(operationContext(c), hex.EncodeToString(sum[:]))
		if err != nil {
			s.problem(c, http.StatusUnauthorized, "invalid_token", "The Agent token is invalid or revoked", false)
			c.Abort()
			return
		}
		c.Set("identity", identity{Token: agent})
		c.Next()
	}
}
func subtleEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for i := range left {
		different |= left[i] ^ right[i]
	}
	return different == 0
}
func requireDesktop() gin.HandlerFunc {
	return func(c *gin.Context) {
		value, _ := c.Get("identity")
		id, _ := value.(identity)
		if !id.Desktop {
			problem(c, http.StatusForbidden, "desktop_required", "This operation is available only to the desktop session", false)
			c.Abort()
			return
		}
		c.Next()
	}
}
func (s *Server) requireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, _ := c.Get("identity")
		id, _ := value.(identity)
		if id.Desktop {
			c.Next()
			return
		}
		for _, candidate := range id.Token.Scopes {
			if candidate == scope {
				c.Next()
				return
			}
		}
		s.problem(c, http.StatusForbidden, "scope_required", "Token does not include the required scope", false)
		c.Abort()
	}
}

func problem(c *gin.Context, status int, code, detail string, retry bool) {
	requestID, _ := c.Get("requestID")
	c.AbortWithStatusJSON(status, model.Problem{Type: "https://knowledge-agent-hub.local/problems/" + code, Title: http.StatusText(status), Status: status, Detail: detail, Code: code, RequestID: fmt.Sprint(requestID), Retryable: retry})
}
func (s *Server) problem(c *gin.Context, status int, code, detail string, retry bool) {
	problem(c, status, code, detail, retry)
}
func bind(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		problem(c, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return false
	}
	return true
}

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "version": s.Config.Version, "worker": s.Worker.State()})
}
func (s *Server) listLibraries(c *gin.Context) {
	items, err := s.Store.ListLibraries(c)
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	c.JSON(200, items)
}
func (s *Server) createLibrary(c *gin.Context) {
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.CreateLibrary(operationContext(c), input.Name, input.Description)
	if err != nil {
		s.problem(c, 400, "library_create_failed", err.Error(), false)
		return
	}
	c.JSON(201, item)
}
func (s *Server) updateLibrary(c *gin.Context) {
	var input struct {
		Name                       *string `json:"name"`
		Description                *string `json:"description"`
		AllowRemoteModels          *bool   `json:"allowRemoteModels"`
		AutoReviewAgentSubmissions *bool   `json:"autoReviewAgentSubmissions"`
		ReviewProviderID           *string `json:"reviewProviderId"`
	}
	if !bind(c, &input) {
		return
	}
	current, err := s.Store.GetLibrary(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, 404, "library_not_found", "Library not found", false)
		return
	}
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	allowRemote := current.AllowRemoteModels
	if input.AllowRemoteModels != nil {
		allowRemote = *input.AllowRemoteModels
	}
	autoReview := current.AutoReviewAgentSubmissions
	if input.AutoReviewAgentSubmissions != nil {
		autoReview = *input.AutoReviewAgentSubmissions
	}
	reviewProviderID := current.ReviewProviderID
	if input.ReviewProviderID != nil {
		reviewProviderID = strings.TrimSpace(*input.ReviewProviderID)
	}
	if autoReview && reviewProviderID == "" {
		s.problem(c, 400, "review_provider_required", "reviewProviderId is required when automatic review is enabled", false)
		return
	}
	if autoReview {
		provider, providerErr := s.Store.GetProvider(operationContext(c), reviewProviderID)
		if errors.Is(providerErr, sql.ErrNoRows) {
			s.problem(c, 400, "review_provider_not_found", "review provider was not found", false)
			return
		}
		if providerErr != nil {
			s.problem(c, 500, "database_error", providerErr.Error(), true)
			return
		}
		if !provider.Local && !allowRemote {
			s.problem(c, 400, "remote_review_not_allowed", "remote review requires allowRemoteModels", false)
			return
		}
	}
	item, err := s.Store.UpdateLibrary(operationContext(c), c.Param("id"), input.Name, input.Description, input.AllowRemoteModels, input.AutoReviewAgentSubmissions, input.ReviewProviderID)
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, 404, "library_not_found", "Library not found", false)
		return
	}
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "library_updated", item.ID, map[string]any{"allowRemoteModels": item.AllowRemoteModels})
	c.JSON(200, item)
}
func (s *Server) deleteLibrary(c *gin.Context) {
	err := s.Store.DeleteLibrary(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, 404, "library_not_found", "Library not found", false)
		return
	}
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	c.Status(204)
}
func (s *Server) listDocuments(c *gin.Context) {
	items, err := s.Store.ListDocumentsFiltered(operationContext(c), c.Query("libraryId"), c.Query("folderId"), c.Query("favorite") == "true")
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	c.JSON(200, items)
}
func (s *Server) repairDocumentText(ctx context.Context, detail model.DocumentDetail) error {
	if !strings.HasPrefix(detail.MediaType, "text/") && !strings.Contains(strings.ToLower(detail.MediaType), "markdown") {
		return nil
	}
	source := detail.SourcePath
	if source == "" {
		resolved, err := s.Store.Resolve(detail.ObjectPath)
		if err != nil {
			return err
		}
		source = resolved
	}
	if _, err := os.Stat(source); err != nil {
		resolved, resolveErr := s.Store.Resolve(detail.ObjectPath)
		if resolveErr != nil {
			return err
		}
		source = resolved
	}
	chunks, err := s.parseDocument(ctx, detail.Document, source)
	if err != nil {
		return err
	}
	if err := s.Store.ReplaceChunks(ctx, detail.ID, chunks); err != nil {
		_ = s.Store.FailDocument(ctx, detail.ID, err)
		return err
	}
	if s.Worker != nil {
		if err := s.Worker.Call(ctx, "index_upsert", map[string]any{"libraryId": detail.LibraryID, "documentId": detail.ID, "chunks": chunks}, nil); err != nil {
			_ = s.Store.FailDocument(ctx, detail.ID, err)
			return err
		}
	}
	return nil
}

func (s *Server) getDocument(c *gin.Context) {
	item, err := s.Store.GetDocument(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, 404, "document_not_found", "Document not found", false)
		return
	}
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	needsRepair, repairErr := s.Store.DocumentNeedsTextRepair(operationContext(c), item.ID)
	if repairErr == nil && needsRepair {
		if repairErr = s.repairDocumentText(operationContext(c), item); repairErr == nil {
			item, err = s.Store.GetDocument(operationContext(c), c.Param("id"))
			if err != nil {
				s.problem(c, 500, "database_error", err.Error(), true)
				return
			}
		}
	}
	if repairErr != nil && s.Logger != nil {
		s.Logger.Warn("document text repair failed", "documentId", item.ID, "error", repairErr)
	}
	c.JSON(200, item)
}
func (s *Server) updateDocument(c *gin.Context) {
	var input struct {
		Title    *string  `json:"title"`
		Tags     []string `json:"tags"`
		Content  *string  `json:"content"`
		Favorite *bool    `json:"favorite"`
	}
	if !bind(c, &input) {
		return
	}
	if input.Content != nil {
		if err := s.updateTextContent(operationContext(c), c.Param("id"), *input.Content); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				s.problem(c, http.StatusNotFound, "document_not_found", "Document not found", false)
			} else {
				s.problem(c, 400, "content_update_failed", err.Error(), false)
			}
			return
		}
	}
	item, err := s.Store.UpdateDocument(operationContext(c), c.Param("id"), input.Title, input.Tags, input.Favorite)
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "document_not_found", "Document not found", false)
		return
	}
	if err != nil {
		s.problem(c, 500, "document_update_failed", err.Error(), true)
		return
	}
	c.JSON(200, item)
}
func (s *Server) deleteDocument(c *gin.Context) {
	err := s.Store.DeleteDocument(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, 404, "document_not_found", "Document not found", false)
		return
	}
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	c.Status(204)
}

func (s *Server) listSkills(c *gin.Context) {
	items, err := s.Store.ListSkills(c)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) getSkill(c *gin.Context) {
	item, err := s.Store.GetSkill(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "skill_not_found", "Skill not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) importSkill(c *gin.Context) {
	var input struct {
		Path    string `json:"path"`
		Replace bool   `json:"replace"`
	}
	if !bind(c, &input) {
		return
	}
	if strings.TrimSpace(input.Path) == "" {
		s.problem(c, http.StatusBadRequest, "invalid_skill_import", "path is required", false)
		return
	}
	job, err := s.Store.CreateJob(operationContext(c), "skill_import", input)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "job_create_failed", err.Error(), true)
		return
	}
	go s.runSkillImport(job.ID, input.Path, input.Replace)
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_import_requested", input.Path, map[string]any{"replace": input.Replace, "jobId": job.ID})
	c.JSON(http.StatusAccepted, job)
}

func (s *Server) runSkillImport(jobID, source string, replace bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.1, "Validating Skill package")
	item, err := s.Store.ImportSkill(ctx, source, replace)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Imported Skill "+item.Name)
}

func (s *Server) updateSkillLinks(c *gin.Context) {
	var input struct {
		UsesLibraryIDs     []string `json:"usesLibraryIds"`
		RequiresLibraryIDs []string `json:"requiresLibraryIds"`
	}
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.SetSkillLinks(operationContext(c), c.Param("id"), input.UsesLibraryIDs, input.RequiresLibraryIDs)
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "skill_or_library_not_found", "Skill or library not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "skill_links_update_failed", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_links_updated", item.ID, map[string]any{"usesLibraryIds": item.UsesLibraryIDs, "requiresLibraryIds": item.RequiresLibraryIDs})
	c.JSON(http.StatusOK, item)
}

func (s *Server) deleteSkill(c *gin.Context) {
	err := s.Store.DeleteSkill(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "skill_not_found", "Skill not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "skill_delete_failed", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_deleted", c.Param("id"), nil)
	c.Status(http.StatusNoContent)
}

func (s *Server) querySkills(c *gin.Context) {
	var req model.SkillQueryRequest
	if !bind(c, &req) {
		return
	}
	req.Defaults()
	if req.TopK < 1 || req.TopK > 100 {
		s.problem(c, http.StatusBadRequest, "invalid_skill_query", "topK must be between 1 and 100", false)
		return
	}
	libraries, ok := s.authorizeLibraries(c, req.LibraryIDs)
	if !ok {
		return
	}
	req.LibraryIDs = libraries
	items, err := s.Store.SearchSkills(operationContext(c), req)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "skill_query_failed", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_query", "", map[string]any{"query": req.Query, "count": len(items)})
	c.JSON(http.StatusOK, model.SkillQueryResponse{RequestID: uuid.NewString(), Skills: items})
}

func (s *Server) skillManifest(c *gin.Context) {
	if !s.authorizeSkill(c, c.Param("id")) {
		return
	}
	item, err := s.Store.GetSkill(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "skill_not_found", "Skill not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	files, err := s.Store.SkillFiles(operationContext(c), item.ID)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "skill_files_failed", err.Error(), true)
		return
	}
	entryTarget, entry, err := s.Store.ReadSkillFile(operationContext(c), item.ID, item.EntryPoint)
	if err != nil {
		s.problem(c, http.StatusNotFound, "skill_entrypoint_missing", "SKILL.md is missing", false)
		return
	}
	content, err := os.ReadFile(entryTarget)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "skill_read_failed", err.Error(), true)
		return
	}
	entry.Content = string(content)
	entry.URL = skillFileURL(item.ID, item.EntryPoint)
	for i := range files {
		files[i].URL = skillFileURL(item.ID, files[i].Path)
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "skill_manifest_read", item.ID, nil)
	c.JSON(http.StatusOK, model.SkillManifest{Skill: item, Root: item.Name + "/", EntryPoint: entry, Files: files})
}

func skillFileURL(id, relative string) string {
	parts := strings.Split(relative, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return fmt.Sprintf("/api/v1/skills/%s/files/%s", url.PathEscape(id), strings.Join(parts, "/"))
}

func (s *Server) skillFile(c *gin.Context) {
	if !s.authorizeSkill(c, c.Param("id")) {
		return
	}
	target, file, err := s.Store.ReadSkillFile(operationContext(c), c.Param("id"), strings.TrimPrefix(c.Param("path"), "/"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "skill_file_not_found", "Skill file not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusBadRequest, "invalid_skill_file_path", err.Error(), false)
		return
	}
	c.Header("Content-Type", file.MediaType)
	c.Header("Cache-Control", "no-store")
	c.File(target)
}

func (s *Server) updateTextContent(ctx context.Context, id, content string) error {
	detail, err := s.Store.GetDocument(ctx, id)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(detail.MediaType, "text/") && detail.MediaType != "text/markdown" {
		return errors.New("only Markdown and text documents can be edited")
	}
	relative, digest, err := s.Store.PutObject(strings.NewReader(content))
	if err != nil {
		return err
	}
	if err := s.Store.UpdateDocumentContent(ctx, id, relative, digest); err != nil {
		return err
	}
	chunks := fallbackChunks(id, content, map[string]any{"kind": "text"})
	for i := range chunks {
		chunks[i].ContentHash = digest
	}
	if err := s.Store.ReplaceChunks(ctx, id, chunks); err != nil {
		return err
	}
	if s.Worker != nil {
		if err := s.Worker.Call(ctx, "index_upsert", map[string]any{"libraryId": detail.LibraryID, "documentId": id, "chunks": chunks}, nil); err != nil {
			_ = s.Store.FailDocument(ctx, id, err)
			return err
		}
	}
	return nil
}

type parseResult struct {
	Title     string `json:"title"`
	MediaType string `json:"mediaType"`
	Chunks    []struct {
		ID          string         `json:"id"`
		Text        string         `json:"text"`
		Location    map[string]any `json:"location"`
		ContentHash string         `json:"contentHash"`
	} `json:"chunks"`
}

func (s *Server) importFiles(c *gin.Context) {
	var input struct {
		LibraryID string   `json:"libraryId"`
		Paths     []string `json:"paths"`
		Watch     bool     `json:"watchSource"`
	}
	if !bind(c, &input) {
		return
	}
	if input.LibraryID == "" || len(input.Paths) == 0 {
		s.problem(c, 400, "invalid_import", "libraryId and paths are required", false)
		return
	}
	jobs := []model.Job{}
	for _, path := range input.Paths {
		clean, err := filepath.Abs(filepath.Clean(path))
		if err != nil {
			continue
		}
		info, err := os.Stat(clean)
		if err != nil || info.IsDir() {
			continue
		}
		job, err := s.Store.CreateJob(operationContext(c), "file_import", map[string]any{"libraryId": input.LibraryID, "path": clean, "name": filepath.Base(clean)})
		if err != nil {
			continue
		}
		jobs = append(jobs, job)
		go s.runFileImport(job.ID, input.LibraryID, clean)
	}
	if len(jobs) == 0 {
		s.problem(c, 400, "no_importable_files", "No readable files were selected", false)
		return
	}
	c.JSON(202, jobs)
}
func (s *Server) runFileImport(jobID, libraryID, path string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.1, "Copying source")
	relative, digest, err := s.putFileWithRetry(ctx, path)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if handled, syncErr := s.syncExistingSourceFile(ctx, jobID, libraryID, path, relative, digest); handled {
		if syncErr != nil {
			s.failJob(ctx, jobID, syncErr)
		}
		return
	}
	if existing, lookupErr := s.Store.FindDocumentByHash(ctx, libraryID, digest); lookupErr == nil {
		needsRepair, repairErr := s.Store.DocumentNeedsTextRepair(ctx, existing.ID)
		if repairErr != nil {
			s.failJob(ctx, jobID, repairErr)
			return
		}
		if needsRepair {
			_ = s.Store.UpdateJob(ctx, jobID, "running", 0.8, "Repairing text encoding")
			detail, detailErr := s.Store.GetDocument(ctx, existing.ID)
			if detailErr != nil {
				s.failJob(ctx, jobID, detailErr)
				return
			}
			if repairErr = s.repairDocumentText(ctx, detail); repairErr != nil {
				s.failJob(ctx, jobID, repairErr)
				return
			}
			_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Repaired "+existing.Title)
			return
		}
		if renamed, renameErr := s.restoreRenamedSource(ctx, jobID, path, existing); renamed {
			if renameErr != nil {
				s.failJob(ctx, jobID, renameErr)
			}
			return
		}
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Deduplicated "+existing.Title)
		return
	}
	mediaType := mediaTypeFor(path)
	doc, err := s.Store.CreatePendingDocument(ctx, libraryID, filepath.Base(path), mediaType, path, "", relative, digest)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.35, "Extracting document")
	resolved, err := s.Store.Resolve(relative)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	chunks, err := s.parseDocument(ctx, doc, resolved)
	if err != nil {
		_ = s.Store.FailDocument(ctx, doc.ID, err)
		s.failJob(ctx, jobID, err)
		return
	}
	if err := s.Store.ReplaceChunks(ctx, doc.ID, chunks); err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if s.Worker != nil {
		if err := s.Worker.Call(ctx, "index_upsert", map[string]any{"libraryId": libraryID, "documentId": doc.ID, "chunks": chunks}, nil); err != nil {
			_ = s.Store.FailDocument(ctx, doc.ID, err)
			s.failJob(ctx, jobID, err)
			return
		}
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Imported "+doc.Title)
}
func (s *Server) parseDocument(ctx context.Context, doc model.Document, path string) ([]model.Chunk, error) {
	if s.Worker != nil {
		var result parseResult
		err := s.Worker.Call(ctx, "parse", map[string]any{"path": path, "documentId": doc.ID, "mediaType": doc.MediaType, "originalName": doc.SourcePath}, &result)
		if err == nil {
			chunks := make([]model.Chunk, 0, len(result.Chunks))
			for _, value := range result.Chunks {
				chunks = append(chunks, model.Chunk{ID: value.ID, DocumentID: doc.ID, Text: value.Text, Location: value.Location, ContentHash: value.ContentHash})
			}
			return chunks, nil
		}
	}
	if strings.HasPrefix(doc.MediaType, "text/") || isCode(doc.SourcePath) {
		bytes, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		text, _, err := decodeTextBytes(bytes)
		if err != nil {
			return nil, err
		}
		return fallbackChunks(doc.ID, text, map[string]any{"kind": "text"}), nil
	}
	return nil, errors.New("document worker is unavailable for this format")
}
func fallbackChunks(documentID, text string, location map[string]any) []model.Chunk {
	runes := []rune(text)
	items := []model.Chunk{}
	for start, ordinal := 0, 0; start < len(runes); ordinal++ {
		end := start + 1200
		if end > len(runes) {
			end = len(runes)
		}
		for end < len(runes) && end > start+600 && runes[end-1] != '\n' {
			end--
		}
		if end <= start {
			end = start + 1200
			if end > len(runes) {
				end = len(runes)
			}
		}
		part := strings.TrimSpace(string(runes[start:end]))
		if part != "" {
			sum := sha256.Sum256([]byte(part))
			loc := map[string]any{}
			for k, v := range location {
				loc[k] = v
			}
			loc["ordinal"] = ordinal
			items = append(items, model.Chunk{ID: uuid.NewString(), DocumentID: documentID, Text: part, Location: loc, ContentHash: hex.EncodeToString(sum[:])})
		}
		start = end
	}
	return items
}
func isCode(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".java", ".cs", ".rs", ".cpp", ".c", ".h", ".json", ".yaml", ".yml", ".toml", ".xml", ".sql", ".sh", ".ps1":
		return true
	}
	return false
}

func mediaTypeFor(path string) string {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".md", ".markdown":
		return "text/markdown"
	case ".yaml", ".yml":
		return "text/yaml"
	case ".csv":
		return "text/csv"
	case ".tsv":
		return "text/tab-separated-values"
	case ".txt", ".rst", ".log", ".ini", ".conf", ".properties":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".java", ".cs", ".rs", ".cpp", ".c", ".h", ".sql", ".sh", ".ps1":
		return "text/plain"
	}
	if value := mime.TypeByExtension(extension); value != "" {
		return value
	}
	return "application/octet-stream"
}
func (s *Server) failJob(ctx context.Context, id string, err error) {
	_ = s.Store.UpdateJob(ctx, id, "failed", 0, err.Error())
	if s.Logger != nil {
		s.Logger.Error("job failed", "jobId", id, "error", err)
	}
}

func (s *Server) importURL(c *gin.Context) {
	var input struct {
		LibraryID string `json:"libraryId"`
		URL       string `json:"url"`
		MaxDepth  int    `json:"maxDepth"`
		MaxPages  int    `json:"maxPages"`
	}
	if !bind(c, &input) {
		return
	}
	parsed, err := url.Parse(input.URL)
	if input.MaxDepth < 0 || input.MaxDepth > 3 {
		s.problem(c, 400, "invalid_crawl", "maxDepth must be between 0 and 3", false)
		return
	}
	if input.MaxPages == 0 {
		input.MaxPages = 1
	}
	if input.MaxPages < 1 || input.MaxPages > 100 {
		s.problem(c, 400, "invalid_crawl", "maxPages must be between 1 and 100", false)
		return
	}
	if err != nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		s.problem(c, 400, "invalid_url", "A valid HTTP or HTTPS URL is required", false)
		return
	}
	job, err := s.Store.CreateJob(operationContext(c), "url_import", input)
	if err != nil {
		s.problem(c, 500, "job_create_failed", err.Error(), true)
		return
	}
	go s.runURLImport(job.ID, input.LibraryID, input.URL, input.MaxDepth, input.MaxPages)
	c.JSON(202, job)
}
func (s *Server) runURLImport(jobID, libraryID, target string, maxDepth, maxPages int) {
	s.runURLImportControlled(jobID, libraryID, target, maxDepth, maxPages)
}
func (s *Server) listJobs(c *gin.Context) {
	items, err := s.Store.ListJobs(c)
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	c.JSON(200, items)
}

func (s *Server) listSavedSearches(c *gin.Context) {
	items, err := s.Store.ListSavedSearches(c)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) createSavedSearch(c *gin.Context) {
	var input model.SavedSearch
	if !bind(c, &input) {
		return
	}
	if len(input.Name) > 120 || len(input.Query) > 8000 {
		s.problem(c, http.StatusBadRequest, "invalid_saved_search", "name or query is too long", false)
		return
	}
	item, err := s.Store.CreateSavedSearch(operationContext(c), input.Name, input.Query, input.LibraryIDs, input.Tags)
	if err != nil {
		s.problem(c, http.StatusBadRequest, "invalid_saved_search", err.Error(), false)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "saved_search_created", item.ID, item)
	c.JSON(http.StatusCreated, item)
}

func (s *Server) deleteSavedSearch(c *gin.Context) {
	err := s.Store.DeleteSavedSearch(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "saved_search_not_found", "Saved search not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) authorizeLibraries(c *gin.Context, requested []string) ([]string, bool) {
	value, _ := c.Get("identity")
	id, _ := value.(identity)
	if id.Desktop {
		return requested, true
	}
	allowed := id.Token.LibraryIDs
	if len(allowed) == 0 {
		return requested, true
	}
	if len(requested) == 0 {
		return allowed, true
	}
	set := map[string]bool{}
	for _, candidate := range allowed {
		set[candidate] = true
	}
	for _, candidate := range requested {
		if !set[candidate] {
			s.problem(c, 403, "library_forbidden", "Token cannot access the requested library", false)
			return nil, false
		}
	}
	return requested, true
}
func (s *Server) runQuery(c *gin.Context, req model.QueryRequest) (model.QueryResponse, error) {
	req.Defaults()
	if req.TopK < 1 || req.TopK > 100 {
		return model.QueryResponse{}, errors.New("topK must be between 1 and 100")
	}
	libraries, ok := s.authorizeLibraries(c, req.LibraryIDs)
	if !ok {
		return model.QueryResponse{}, errors.New("authorization already handled")
	}
	req.LibraryIDs = libraries
	evidence, degraded, degradedReason, err := s.searchEvidence(operationContext(c), req)
	if err != nil {
		return model.QueryResponse{}, err
	}
	requiredLibraryIDs := append([]string{}, req.LibraryIDs...)
	if len(requiredLibraryIDs) == 0 {
		seen := map[string]bool{}
		for _, item := range evidence {
			if !seen[item.LibraryID] {
				requiredLibraryIDs = append(requiredLibraryIDs, item.LibraryID)
				seen[item.LibraryID] = true
			}
		}
	}
	requiredSkills, err := s.Store.RequiredSkills(operationContext(c), requiredLibraryIDs)
	if err != nil {
		return model.QueryResponse{}, err
	}
	response := model.QueryResponse{RequestID: uuid.NewString(), Evidence: evidence, RequiredSkills: requiredSkills, Degraded: degraded, DegradedReason: degradedReason}
	if req.ResponseMode == "answer" {
		if req.ProviderID == "" {
			return response, errors.New("providerId is required for answer mode")
		}
		provider, err := s.Store.GetProvider(operationContext(c), req.ProviderID)
		if err != nil {
			return response, err
		}
		if !provider.Local {
			actual := req.LibraryIDs
			if len(actual) == 0 {
				set := map[string]bool{}
				for _, item := range evidence {
					set[item.LibraryID] = true
				}
				for id := range set {
					actual = append(actual, id)
				}
			}
			allowed, err := s.Store.LibrariesAllowRemote(operationContext(c), actual)
			if err != nil || !allowed {
				return response, errors.New("remote model access is not allowed for every selected library")
			}
		}
		key, err := secrets.Get("provider:" + provider.ID)
		if err != nil {
			return response, err
		}
		answer, err := s.Providers.Generate(operationContext(c), provider, key, req.Query, evidence)
		if err != nil {
			return response, err
		}
		response.Answer = answer
	}
	if err := s.Store.RecordQueryEvidence(operationContext(c), response.RequestID, response.Evidence); err != nil {
		return response, err
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "query_completed", response.RequestID, map[string]any{"responseMode": req.ResponseMode, "providerId": req.ProviderID, "evidenceCount": len(response.Evidence), "degraded": response.Degraded})
	return response, nil
}

type workerSearchResponse struct {
	Results []struct {
		ID      string  `json:"id"`
		Lexical float64 `json:"lexical"`
		Vector  float64 `json:"vector"`
		Fusion  float64 `json:"fusion"`
		Final   float64 `json:"final"`
	} `json:"results"`
}

func (s *Server) searchEvidence(ctx context.Context, req model.QueryRequest) ([]model.Evidence, bool, string, error) {
	if s.Worker == nil || s.Worker.State() != "ok" {
		items, err := s.Store.Search(ctx, req)
		return items, true, "Index worker unavailable; lexical SQLite fallback was used", err
	}
	var result workerSearchResponse
	err := s.Worker.Call(ctx, "search", map[string]any{
		"query": req.Query, "libraryIds": req.LibraryIDs, "topK": req.TopK, "retrievalMode": req.RetrievalMode,
	}, &result)
	if err != nil {
		items, fallbackErr := s.Store.Search(ctx, req)
		if fallbackErr != nil {
			return nil, true, "", fmt.Errorf("worker search failed: %w; lexical fallback failed: %v", err, fallbackErr)
		}
		return items, true, "Index worker search failed; lexical SQLite fallback was used", nil
	}
	ids := make([]string, 0, len(result.Results))
	for _, item := range result.Results {
		ids = append(ids, item.ID)
	}
	hydrated, err := s.Store.EvidenceByChunkIDs(ctx, ids)
	if err != nil {
		return nil, false, "", err
	}
	evidence := make([]model.Evidence, 0, len(result.Results))
	for _, item := range result.Results {
		value, ok := hydrated[item.ID]
		if !ok {
			continue
		}
		value.Scores = model.Scores{Lexical: item.Lexical, Vector: item.Vector, Fusion: item.Fusion, Final: item.Final}
		evidence = append(evidence, value)
	}
	return evidence, false, "", nil
}
func (s *Server) query(c *gin.Context) {
	var req model.QueryRequest
	if !bind(c, &req) {
		return
	}
	response, err := s.runQuery(c, req)
	if err != nil {
		if c.IsAborted() {
			return
		}
		s.problem(c, 400, "query_failed", err.Error(), false)
		return
	}
	c.JSON(200, response)
}
func (s *Server) queryStream(c *gin.Context) {
	var req model.QueryRequest
	if !bind(c, &req) {
		return
	}
	answerMode := req.ResponseMode == "answer"
	requestedProviderID := req.ProviderID
	req.ResponseMode = "evidence"
	response, err := s.runQuery(c, req)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	send := func(event string, data any) error {
		payload, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			return marshalErr
		}
		if _, writeErr := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, payload); writeErr != nil {
			return writeErr
		}
		c.Writer.Flush()
		return nil
	}
	if err != nil {
		if c.IsAborted() {
			return
		}
		_ = send("error", gin.H{"code": "query_failed", "detail": err.Error()})
		return
	}
	if err := send("retrieval", gin.H{"requestId": response.RequestID, "degraded": response.Degraded, "degradedReason": response.DegradedReason, "count": len(response.Evidence), "requiredSkills": response.RequiredSkills}); err != nil {
		return
	}
	for _, item := range response.Evidence {
		if err := send("citation", item); err != nil {
			return
		}
	}
	if !answerMode {
		_ = send("complete", gin.H{"requestId": response.RequestID})
		return
	}
	if requestedProviderID == "" {
		_ = send("error", gin.H{"code": "query_failed", "detail": "providerId is required for answer mode"})
		return
	}
	provider, err := s.Store.GetProvider(operationContext(c), requestedProviderID)
	if err != nil {
		_ = send("error", gin.H{"code": "query_failed", "detail": err.Error()})
		return
	}
	if !provider.Local {
		actual := req.LibraryIDs
		if len(actual) == 0 {
			seen := map[string]bool{}
			for _, item := range response.Evidence {
				if !seen[item.LibraryID] {
					actual = append(actual, item.LibraryID)
					seen[item.LibraryID] = true
				}
			}
		}
		allowed, allowErr := s.Store.LibrariesAllowRemote(operationContext(c), actual)
		if allowErr != nil || !allowed {
			_ = send("error", gin.H{"code": "query_failed", "detail": "remote model access is not allowed for every selected library"})
			return
		}
	}
	key, err := secrets.Get("provider:" + provider.ID)
	if err != nil {
		_ = send("error", gin.H{"code": "query_failed", "detail": err.Error()})
		return
	}
	if err := s.Providers.GenerateStream(operationContext(c), provider, key, req.Query, response.Evidence, func(delta string) error {
		return send("answer_delta", gin.H{"text": delta})
	}); err != nil {
		_ = send("error", gin.H{"code": "query_failed", "detail": err.Error()})
		return
	}
	_ = send("complete", gin.H{"requestId": response.RequestID})
}
func (s *Server) feedback(c *gin.Context) {
	var input struct {
		RequestID string `json:"requestId"`
		ChunkID   string `json:"chunkId"`
		Relevant  bool   `json:"relevant"`
		Note      string `json:"note"`
	}
	if !bind(c, &input) {
		return
	}
	if input.RequestID == "" || input.ChunkID == "" {
		s.problem(c, 400, "invalid_feedback", "requestId and chunkId are required", false)
		return
	}
	libraryID, err := s.Store.ChunkLibrary(operationContext(c), input.ChunkID)
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "chunk_not_found", "Chunk not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	if _, ok := s.authorizeLibraries(c, []string{libraryID}); !ok {
		return
	}
	associated, err := s.Store.QueryEvidenceContains(operationContext(c), input.RequestID, input.ChunkID)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "feedback_check_failed", err.Error(), true)
		return
	}
	if !associated {
		s.problem(c, http.StatusBadRequest, "invalid_feedback", "chunkId was not returned for requestId", false)
		return
	}
	if len(input.Note) > 1000 {
		input.Note = input.Note[:1000]
	}
	if err := s.Store.AddFeedback(operationContext(c), input.RequestID, input.ChunkID, input.Relevant, input.Note); err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "feedback_submitted", input.ChunkID, map[string]any{"requestId": input.RequestID, "relevant": input.Relevant})
	c.Status(204)
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "kah_" + base64.RawURLEncoding.EncodeToString(bytes), nil
}
func (s *Server) createToken(c *gin.Context) {
	var input struct {
		Name       string   `json:"name"`
		Scopes     []string `json:"scopes"`
		LibraryIDs []string `json:"libraryIds"`
	}
	if !bind(c, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Scopes) == 0 {
		s.problem(c, 400, "invalid_token", "name and scopes are required", false)
		return
	}
	for _, scope := range input.Scopes {
		if scope != "query" && scope != "feedback" && scope != "submit" {
			s.problem(c, 400, "invalid_scope", "Only query, feedback, and submit scopes are supported", false)
			return
		}
	}
	if contains(input.Scopes, "submit") && len(input.LibraryIDs) == 0 {
		s.problem(c, 400, "submit_library_required", "submit tokens must be bound to at least one library", false)
		return
	}
	for index, libraryID := range input.LibraryIDs {
		input.LibraryIDs[index] = strings.TrimSpace(libraryID)
		if input.LibraryIDs[index] == "" {
			s.problem(c, 400, "invalid_token", "libraryIds must contain non-empty IDs", false)
			return
		}
	}
	if err := s.Store.ValidateLibraryIDs(operationContext(c), input.LibraryIDs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.problem(c, 400, "invalid_token", "One or more libraryIds do not exist", false)
			return
		}
		s.problem(c, 500, "token_create_failed", err.Error(), true)
		return
	}
	secret, err := randomToken()
	if err != nil {
		s.problem(c, 500, "token_create_failed", err.Error(), true)
		return
	}
	sum := sha256.Sum256([]byte(secret))
	item, err := s.Store.CreateToken(operationContext(c), input.Name, hex.EncodeToString(sum[:]), input.Scopes, input.LibraryIDs)
	if err != nil {
		s.problem(c, 500, "token_create_failed", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "token_created", item.ID, map[string]any{"scopes": input.Scopes, "libraryCount": len(input.LibraryIDs)})
	item.Secret = secret
	c.JSON(201, item)
}
func (s *Server) listTokens(c *gin.Context) {
	items, err := s.Store.ListTokens(c)
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	c.JSON(200, items)
}
func (s *Server) revokeToken(c *gin.Context) {
	err := s.Store.RevokeToken(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, 404, "token_not_found", "Token not found or already revoked", false)
		return
	}
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "token_revoked", c.Param("id"), nil)
	c.Status(204)
}
func (s *Server) listProviders(c *gin.Context) {
	items, err := s.Store.ListProviders(c)
	if err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	c.JSON(200, items)
}
func providerIsLocal(kind string, parsed *url.URL) bool {
	if kind != "lmstudio" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
func (s *Server) saveProvider(c *gin.Context) {
	var provider model.Provider
	if !bind(c, &provider) {
		return
	}
	if strings.TrimSpace(provider.Name) == "" {
		s.problem(c, 400, "invalid_provider", "Provider name is required", false)
		return
	}
	provider.Name = strings.TrimSpace(provider.Name)
	if provider.ID == "" {
		provider.ID = uuid.NewString()
	}
	if provider.Kind != "openai" && provider.Kind != "anthropic" && provider.Kind != "lmstudio" && provider.Kind != "custom" {
		s.problem(c, 400, "invalid_provider", "Unsupported provider kind", false)
		return
	}
	provider.BaseURL = strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	parsed, err := url.Parse(provider.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		s.problem(c, 400, "invalid_provider_url", "Provider URL must use HTTP or HTTPS", false)
		return
	}
	provider.Local = providerIsLocal(provider.Kind, parsed)
	if provider.APIKey != "" {
		if err := secrets.Set("provider:"+provider.ID, provider.APIKey); err != nil {
			s.problem(c, 500, "secret_store_failed", err.Error(), false)
			return
		}
	}
	provider.APIKey = ""
	if err := s.Store.SaveProvider(operationContext(c), provider); err != nil {
		s.problem(c, 500, "database_error", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "provider_saved", provider.ID, map[string]any{"kind": provider.Kind, "local": provider.Local})
	c.JSON(200, provider)
}
func (s *Server) listProviderModels(c *gin.Context) {
	provider, err := s.Store.GetProvider(operationContext(c), c.Param("id"))
	if err != nil {
		s.problem(c, 404, "provider_not_found", "Provider not found", false)
		return
	}
	key, err := secrets.Get("provider:" + provider.ID)
	if err != nil {
		s.problem(c, 500, "secret_store_failed", err.Error(), false)
		return
	}
	models, err := s.Providers.Models(operationContext(c), provider, key)
	if err != nil {
		s.problem(c, 502, "provider_unavailable", err.Error(), true)
		return
	}
	c.JSON(200, gin.H{"models": models})
}
func (s *Server) listAudit(c *gin.Context) {
	items, err := s.Store.ListAudit(operationContext(c), 200)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}
func (s *Server) createBackup(c *gin.Context) {
	var input struct {
		IncludeIndexes *bool `json:"includeIndexes"`
	}
	if c.Request.ContentLength > 0 && !bind(c, &input) {
		return
	}
	include := true
	if input.IncludeIndexes != nil {
		include = *input.IncludeIndexes
	}
	path, digest, err := s.Store.CreateBackup(operationContext(c), include)
	if err != nil {
		s.problem(c, 500, "backup_failed", err.Error(), true)
		return
	}
	_ = s.Store.AddAudit(operationContext(c), actorName(c), "backup_created", path, map[string]any{"includeIndexes": include, "sha256": digest})
	c.JSON(201, gin.H{"path": path, "sha256": digest})
}

func operationContext(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return context.Background()
	}
	return c.Request.Context()
}

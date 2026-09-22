package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/worker"
	"github.com/gin-gonic/gin"
)

func (s *Server) retrievalScope(ctx context.Context, requestedLibraries, requestedProjects []string) ([]string, []string, error) {
	libraries, err := s.Store.ListLibraries(ctx)
	if err != nil {
		return nil, nil, err
	}
	available := make(map[string]bool, len(libraries))
	for _, item := range libraries {
		available[item.ID] = true
	}
	allowedLibraries := uniqueNonEmpty(requestedLibraries)
	if len(allowedLibraries) == 0 {
		for _, item := range libraries {
			allowedLibraries = append(allowedLibraries, item.ID)
		}
	}
	for _, id := range allowedLibraries {
		if !available[id] {
			return nil, nil, errors.New("retrieval library is unavailable or deleted")
		}
	}
	librarySet := make(map[string]bool, len(allowedLibraries))
	for _, id := range allowedLibraries {
		librarySet[id] = true
	}
	allowedProjects := uniqueNonEmpty(requestedProjects)
	if len(allowedProjects) == 0 {
		for _, libraryID := range allowedLibraries {
			projects, listErr := s.Store.ListProjects(ctx, libraryID)
			if listErr != nil {
				return nil, nil, listErr
			}
			for _, project := range projects {
				allowedProjects = append(allowedProjects, project.ID)
			}
		}
	} else {
		for _, projectID := range allowedProjects {
			project, getErr := s.Store.GetProject(ctx, projectID)
			if getErr != nil || !librarySet[project.LibraryID] {
				return nil, nil, errors.New("retrieval project is unavailable or outside the selected libraries")
			}
		}
	}
	return allowedLibraries, allowedProjects, nil
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func toWorkerRetrievalChunk(item model.RetrievalChunk) worker.RetrievalChunk {
	return worker.RetrievalChunk{
		ID: item.ID, DocumentID: item.DocumentID, Text: item.Text, SourceType: item.SourceType, ReviewStatus: item.ReviewStatus,
		LibraryID: item.LibraryID, ProjectID: item.ProjectID, SourceURI: item.SourceURI, Title: item.Title, FilePath: item.FilePath,
		LineStart: item.LineStart, LineEnd: item.LineEnd, ContentHash: item.ContentHash, Symbol: item.Symbol, Language: item.Language,
		PermissionScope: item.PermissionScope, Location: item.Location, TokenCount: item.TokenCount, TokenLimit: item.TokenLimit, Truncated: item.Truncated,
	}
}

func fromWorkerRetrievalResult(item worker.RetrievalResult) model.RetrievalResult {
	return model.RetrievalResult{
		RetrievalChunk: model.RetrievalChunk{
			ID: item.ID, DocumentID: item.DocumentID, Text: item.Text, SourceType: item.SourceType, ReviewStatus: item.ReviewStatus,
			LibraryID: item.LibraryID, ProjectID: item.ProjectID, SourceURI: item.SourceURI, Title: item.Title, FilePath: item.FilePath,
			LineStart: item.LineStart, LineEnd: item.LineEnd, ContentHash: item.ContentHash, Symbol: item.Symbol, Language: item.Language,
			PermissionScope: item.PermissionScope, Location: item.Location, TokenCount: item.TokenCount, TokenLimit: item.TokenLimit, Truncated: item.Truncated,
		},
		Scores: model.RetrievalScores{Lexical: item.Lexical, Vector: item.Vector, Fusion: item.Fusion, Final: item.Final, SymbolMatch: item.SymbolMatch},
	}
}

// retrievalResultsToEvidence preserves the long-standing /query response
// shape while carrying the unified source metadata needed by desktop answer
// citations. The retrieval service remains the only ranking path; this is
// only a wire-contract adapter for clients that still consume Evidence.
func retrievalResultsToEvidence(items []model.RetrievalResult) []model.Evidence {
	result := make([]model.Evidence, 0, len(items))
	for _, item := range items {
		result = append(result, model.Evidence{
			Chunk:     model.Chunk{ID: item.ID, DocumentID: item.DocumentID, Text: item.Text, Location: item.Location, ContentHash: item.ContentHash},
			LibraryID: item.LibraryID, Title: item.Title, SourceType: item.SourceType, ReviewStatus: item.ReviewStatus,
			ProjectID: item.ProjectID, SourceURI: item.SourceURI, FilePath: item.FilePath, LineStart: item.LineStart,
			LineEnd: item.LineEnd, PermissionScope: item.PermissionScope, Language: item.Language, Symbol: item.Symbol,
			Scores: model.Scores{Lexical: item.Scores.Lexical, Vector: item.Scores.Vector, Fusion: item.Scores.Fusion, Final: item.Scores.Final},
		})
	}
	return result
}

func retrievalFilterAllows(item model.RetrievalChunk, filters model.RetrievalFilters, allowedLibraries, allowedProjects map[string]bool) bool {
	if !allowedLibraries[item.LibraryID] || item.ReviewStatus == "" {
		return false
	}
	if filters.SourceType != "" && filters.SourceType != "all" && item.SourceType != filters.SourceType {
		return false
	}
	if len(filters.SourceTypes) > 0 {
		found := false
		for _, sourceType := range filters.SourceTypes {
			if sourceType == item.SourceType {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(filters.ReviewStatuses) > 0 {
		found := false
		for _, status := range filters.ReviewStatuses {
			if status == item.ReviewStatus {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if item.SourceType == "knowledge" && item.ReviewStatus != "stable" {
		return false
	}
	if item.SourceType == "raw" && !filters.AllowRaw {
		return false
	}
	if item.SourceType == "architecture" && !allowedProjects[item.ProjectID] {
		return false
	}
	return true
}

func (s *Server) retrievalSearch(c *gin.Context) {
	var request model.RetrievalSearchRequest
	if !bind(c, &request) {
		return
	}
	if strings.TrimSpace(request.Query) == "" {
		s.problem(c, http.StatusBadRequest, "invalid_query", "query is required", false)
		return
	}
	if request.TopK < 1 || request.TopK > 100 {
		request.TopK = 10
	}
	if request.RetrievalMode == "" {
		request.RetrievalMode = "hybrid"
	}
	sourceType, err := normalizeEvidenceSourceType(request.Filters.SourceType)
	if err != nil {
		s.problem(c, http.StatusBadRequest, "invalid_source_type", err.Error(), false)
		return
	}
	request.Filters.SourceType = sourceType
	sourceTypes, err := normalizeEvidenceSourceTypes(request.Filters.SourceTypes)
	if err != nil {
		s.problem(c, http.StatusBadRequest, "invalid_source_type", err.Error(), false)
		return
	}
	request.Filters.SourceTypes = sourceTypes
	allowedLibraries, allowedProjects, err := s.retrievalScope(operationContext(c), request.Filters.LibraryIDs, request.Filters.ProjectIDs)
	if err != nil {
		s.problem(c, http.StatusForbidden, "retrieval_scope_denied", err.Error(), false)
		return
	}
	// Desktop is authorized for raw evidence in its selected libraries. The
	// client cannot widen the project/library scope because these lists are
	// replaced by the server-calculated values below.
	request.Filters.LibraryIDs = allowedLibraries
	request.Filters.AllowedLibraryIDs = allowedLibraries
	request.Filters.ProjectIDs = nil
	request.Filters.AllowedProjectIDs = allowedProjects
	request.Filters.AllowRaw = true
	allowedLibrarySet := make(map[string]bool, len(allowedLibraries))
	for _, id := range allowedLibraries {
		allowedLibrarySet[id] = true
	}
	allowedProjectSet := make(map[string]bool, len(allowedProjects))
	for _, id := range allowedProjects {
		allowedProjectSet[id] = true
	}

	response := model.RetrievalSearchResponse{Results: []model.RetrievalResult{}, RetrievalMode: request.RetrievalMode}
	if len(allowedLibraries) == 0 {
		response.Source = "empty"
		response.KeywordOnly, response.Degraded = true, true
		response.DegradedReason = "No active libraries are available for retrieval."
		c.JSON(http.StatusOK, response)
		return
	}
	if s.Worker != nil && s.Worker.State() == "ok" {
		var workerResponse worker.RetrievalSearchResponse
		workerRequest := worker.RetrievalSearchRequest{Query: request.Query, TopK: request.TopK, RetrievalMode: request.RetrievalMode, Filters: worker.RetrievalFilters{
			SourceType: request.Filters.SourceType, SourceTypes: request.Filters.SourceTypes, ReviewStatuses: request.Filters.ReviewStatuses,
			LibraryIDs: allowedLibraries, AllowedLibraryIDs: allowedLibraries, AllowedProjectIDs: allowedProjects, AllowRaw: true,
		}}
		if err = s.Worker.RetrievalSearch(operationContext(c), workerRequest, &workerResponse); err == nil {
			response.Source, response.Generation = workerResponse.Source, workerResponse.Generation
			response.SemanticAvailable, response.KeywordOnly, response.Degraded = workerResponse.SemanticAvailable, workerResponse.KeywordOnly, workerResponse.Degraded
			response.DegradedReason, response.ModelID, response.ModelVersion, response.ConfigHash, response.Dimension = workerResponse.DegradedReason, workerResponse.ModelID, workerResponse.ModelVersion, workerResponse.ConfigHash, workerResponse.Dimension
			response.CorpusComplete, response.NeedsRebuild = workerResponse.CorpusComplete, workerResponse.NeedsRebuild
			response.RetrievalMode = workerResponse.RetrievalMode
			// Hydrate against the current authoritative source set before exposing
			// worker results. This removes deleted/pending documents and checks
			// project/library boundaries even if an index is stale or corrupted.
			corpus, corpusErr := s.Store.BuildRetrievalCorpus(operationContext(c), allowedLibraries)
			if corpusErr != nil {
				s.problem(c, http.StatusInternalServerError, "retrieval_hydration_failed", corpusErr.Error(), true)
				return
			}
			if corpusErr == nil {
				authoritative := make(map[string]model.RetrievalChunk, len(corpus))
				for _, item := range corpus {
					if retrievalFilterAllows(item, request.Filters, allowedLibrarySet, allowedProjectSet) {
						authoritative[item.ID] = item
					}
				}
				for _, item := range workerResponse.Results {
					value, ok := authoritative[item.ID]
					if !ok || value.ContentHash != item.ContentHash || value.LibraryID != item.LibraryID {
						continue
					}
					result := fromWorkerRetrievalResult(item)
					result.RetrievalChunk = value
					result.Scores = model.RetrievalScores{Lexical: item.Lexical, Vector: item.Vector, Fusion: item.Fusion, Final: item.Final, SymbolMatch: item.SymbolMatch}
					response.Results = append(response.Results, result)
				}
			}
			c.JSON(http.StatusOK, response)
			return
		}
	}
	// The API remains useful while the Worker is unavailable. This path is
	// deliberately lexical and says so in the response; no hash vector is
	// presented as semantic retrieval.
	corpus, corpusErr := s.Store.BuildRetrievalCorpus(operationContext(c), allowedLibraries)
	if corpusErr != nil {
		s.problem(c, http.StatusInternalServerError, "retrieval_failed", corpusErr.Error(), true)
		return
	}
	type ranked struct {
		item  model.RetrievalChunk
		score float64
	}
	terms := strings.Fields(strings.ToLower(request.Query))
	items := make([]ranked, 0)
	for _, item := range corpus {
		if !retrievalFilterAllows(item, request.Filters, allowedLibrarySet, allowedProjectSet) {
			continue
		}
		lower := strings.ToLower(item.Text)
		matches := 0
		for _, term := range terms {
			if strings.Contains(lower, term) {
				matches++
			}
		}
		score := float64(matches) / float64(retrievalMaxInt(1, len(terms)))
		if item.Symbol != "" && strings.EqualFold(strings.TrimSpace(request.Query), item.Symbol) {
			score += 2
		}
		if score > 0 {
			items = append(items, ranked{item: item, score: score})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
	for _, value := range items[:retrievalMinInt(request.TopK, len(items))] {
		response.Results = append(response.Results, model.RetrievalResult{RetrievalChunk: value.item, Scores: model.RetrievalScores{Lexical: value.score, Fusion: value.score, Final: value.score}})
	}
	response.Source = "sqlite-keyword"
	response.KeywordOnly, response.Degraded = true, true
	response.DegradedReason = "Retrieval worker unavailable; authoritative SQLite keyword search was used."
	c.JSON(http.StatusOK, response)
}

func (s *Server) retrievalRebuild(c *gin.Context) {
	var input struct {
		LibraryIDs []string `json:"libraryIds"`
		ModelID    string   `json:"modelId,omitempty"`
	}
	if !bind(c, &input) {
		return
	}
	allowedLibraries, _, err := s.retrievalScope(operationContext(c), input.LibraryIDs, nil)
	if err != nil {
		s.problem(c, http.StatusForbidden, "retrieval_scope_denied", err.Error(), false)
		return
	}
	if s.Worker == nil || s.Worker.State() != "ok" {
		s.problem(c, http.StatusServiceUnavailable, "worker_unavailable", "Document worker is unavailable", true)
		return
	}
	results := make([]model.RetrievalGeneration, 0, len(allowedLibraries))
	for _, libraryID := range allowedLibraries {
		if !s.beginRetrievalRebuild(libraryID) {
			s.problem(c, http.StatusConflict, "retrieval_rebuild_active", "a retrieval rebuild is already active for this library", false)
			return
		}
		generations, rebuildErr := s.runRetrievalRebuildLoop(operationContext(c), libraryID, input.ModelID, s.rebuildUnifiedLibrary, func() {
			// A source publication or architecture update arrived while the
			// previous corpus was being materialized. Rebuild the latest corpus.
			_ = s.Store.SetRetrievalIndexState(operationContext(c), libraryID, "queued", "", "")
		})
		if rebuildErr != nil {
			s.problem(c, http.StatusBadRequest, "retrieval_rebuild_failed", rebuildErr.Error(), false)
			return
		}
		results = append(results, generations...)
	}
	c.JSON(http.StatusOK, gin.H{"generations": results})
}

func (s *Server) rebuildUnifiedLibrary(ctx context.Context, libraryID, modelID string) (model.RetrievalGeneration, error) {
	if err := s.Store.SetRetrievalIndexState(ctx, libraryID, "building", "", ""); err != nil {
		return model.RetrievalGeneration{}, err
	}
	if s.Worker == nil || s.Worker.State() != "ok" {
		err := errors.New("document worker is unavailable")
		s.persistRetrievalState(libraryID, "stale", "", err.Error())
		return model.RetrievalGeneration{}, err
	}
	corpus, err := s.Store.BuildRetrievalCorpus(ctx, []string{libraryID})
	if err != nil {
		s.persistRetrievalState(libraryID, "stale", "", err.Error())
		return model.RetrievalGeneration{}, err
	}
	chunks := make([]worker.RetrievalChunk, 0, len(corpus))
	for _, item := range corpus {
		chunks = append(chunks, toWorkerRetrievalChunk(item))
	}
	var generation worker.RetrievalGeneration
	if err = s.Worker.RetrievalRebuild(ctx, worker.RetrievalRebuildRequest{LibraryID: libraryID, Chunks: chunks, ModelID: modelID}, &generation); err != nil {
		s.persistRetrievalState(libraryID, "stale", "", err.Error())
		return model.RetrievalGeneration{}, err
	}
	if err = s.Store.SetRetrievalIndexState(ctx, libraryID, "current", generation.Generation, ""); err != nil {
		return model.RetrievalGeneration{}, err
	}
	return model.RetrievalGeneration{LibraryID: generation.LibraryID, Generation: generation.Generation, Status: "current", Count: generation.Count, SemanticAvailable: generation.SemanticAvailable, KeywordOnly: generation.KeywordOnly, Degraded: generation.Degraded, DegradedReason: generation.DegradedReason, ModelID: generation.ModelID, ModelVersion: generation.ModelVersion, ConfigHash: generation.ConfigHash, Dimension: generation.Dimension, CorpusComplete: generation.CorpusComplete, NeedsRebuild: generation.NeedsRebuild}, nil
}

type retrievalBuildFunc func(context.Context, string, string) (model.RetrievalGeneration, error)

// runRetrievalRebuildLoop coalesces source updates that arrive while a
// generation is being built. The callback is deliberately injectable so the
// state machine can be tested without starting a Python worker.
func (s *Server) runRetrievalRebuildLoop(ctx context.Context, libraryID, modelID string, build retrievalBuildFunc, queue func()) ([]model.RetrievalGeneration, error) {
	generations := make([]model.RetrievalGeneration, 0, 1)
	for {
		generation, err := build(ctx, libraryID, modelID)
		if err != nil {
			s.abortRetrievalRebuild(libraryID)
			return nil, err
		}
		generations = append(generations, generation)
		if !s.finishRetrievalRebuild(libraryID) {
			return generations, nil
		}
		if queue != nil {
			queue()
		}
	}
}

func (s *Server) persistRetrievalState(libraryID, status, generation, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Store.SetRetrievalIndexState(ctx, libraryID, status, generation, message)
}

func (s *Server) refreshUnifiedLibrary(_ context.Context, libraryID string) {
	if strings.TrimSpace(libraryID) == "" || s.Store == nil {
		return
	}
	lifecycle := s.lifecycleContext()
	_ = s.Store.SetRetrievalIndexState(lifecycle, libraryID, "queued", "", "")
	if !s.beginRetrievalRebuild(libraryID) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(lifecycle, 30*time.Minute)
		defer cancel()
		_, err := s.runRetrievalRebuildLoop(ctx, libraryID, "", s.rebuildUnifiedLibrary, func() {
			_ = s.Store.SetRetrievalIndexState(lifecycle, libraryID, "queued", "", "")
		})
		if err != nil {
			if s.Logger != nil {
				s.Logger.Warn("unified retrieval generation is stale", "libraryId", libraryID, "error", err)
			}
		}
	}()
}

// beginRetrievalRebuild starts a per-library rebuild or records that the
// current run must repeat after it has materialized the latest corpus.
func (s *Server) beginRetrievalRebuild(libraryID string) bool {
	s.retrievalMu.Lock()
	defer s.retrievalMu.Unlock()
	if s.retrievalRunning[libraryID] {
		s.retrievalDirty[libraryID] = true
		return false
	}
	s.retrievalRunning[libraryID] = true
	delete(s.retrievalDirty, libraryID)
	return true
}

// finishRetrievalRebuild atomically decides whether a queued source update
// requires another generation. Keeping the running flag while returning true
// closes the race between the final corpus read and a concurrent refresh.
func (s *Server) finishRetrievalRebuild(libraryID string) bool {
	s.retrievalMu.Lock()
	defer s.retrievalMu.Unlock()
	if s.retrievalDirty[libraryID] {
		delete(s.retrievalDirty, libraryID)
		return true
	}
	delete(s.retrievalRunning, libraryID)
	return false
}

func (s *Server) abortRetrievalRebuild(libraryID string) {
	s.retrievalMu.Lock()
	delete(s.retrievalDirty, libraryID)
	delete(s.retrievalRunning, libraryID)
	s.retrievalMu.Unlock()
}

// ResumeRetrievalIndexes schedules work that was queued or building when the
// API process stopped. The old active generation remains queryable until the
// new generation validates and swaps its pointer.
func (s *Server) ResumeRetrievalIndexes(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.SetLifecycleContext(ctx)
	libraries, err := s.Store.ListLibraries(ctx)
	if err != nil {
		return err
	}
	for _, library := range libraries {
		state, stateErr := s.Store.GetRetrievalIndexState(ctx, library.ID)
		if stateErr != nil || (state.Status != "queued" && state.Status != "building" && state.Status != "stale") {
			continue
		}
		s.refreshUnifiedLibrary(ctx, library.ID)
	}
	return nil
}

func (s *Server) retrievalIndexStatus(c *gin.Context) {
	libraryID := strings.TrimSpace(c.Query("libraryId"))
	if libraryID == "" {
		s.problem(c, http.StatusBadRequest, "invalid_library_id", "libraryId is required", false)
		return
	}
	allowed, _, err := s.retrievalScope(operationContext(c), []string{libraryID}, nil)
	if err != nil || len(allowed) != 1 {
		s.problem(c, http.StatusForbidden, "retrieval_scope_denied", "library is unavailable or deleted", false)
		return
	}
	if s.Worker == nil || s.Worker.State() != "ok" {
		s.problem(c, http.StatusServiceUnavailable, "worker_unavailable", "Document worker is unavailable", true)
		return
	}
	var result worker.RetrievalGeneration
	if err := s.Worker.RetrievalStatus(operationContext(c), libraryID, &result); err != nil {
		s.problem(c, http.StatusServiceUnavailable, "retrieval_status_failed", err.Error(), true)
		return
	}
	if state, stateErr := s.Store.GetRetrievalIndexState(operationContext(c), libraryID); stateErr == nil {
		result.Status = state.Status
		if result.DegradedReason == "" {
			result.DegradedReason = state.DegradedReason
		}
		result.Degraded = result.Degraded || state.Degraded
		if state.Degraded {
			result.NeedsRebuild = true
			result.CorpusComplete = false
		}
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) retrievalIndexProgress(c *gin.Context) {
	libraryID := strings.TrimSpace(c.Query("libraryId"))
	if libraryID == "" {
		s.problem(c, http.StatusBadRequest, "invalid_library_id", "libraryId is required", false)
		return
	}
	if _, _, err := s.retrievalScope(operationContext(c), []string{libraryID}, nil); err != nil {
		s.problem(c, http.StatusForbidden, "retrieval_scope_denied", err.Error(), false)
		return
	}
	if s.Worker == nil || s.Worker.State() != "ok" {
		s.problem(c, http.StatusServiceUnavailable, "worker_unavailable", "Document worker is unavailable", true)
		return
	}
	var result worker.RetrievalProgress
	if err := s.Worker.RetrievalProgress(operationContext(c), libraryID, &result); err != nil {
		s.problem(c, http.StatusServiceUnavailable, "retrieval_progress_failed", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) retrievalIndexCancel(c *gin.Context) {
	var input struct {
		LibraryID string `json:"libraryId"`
	}
	if !bind(c, &input) {
		return
	}
	if strings.TrimSpace(input.LibraryID) == "" {
		s.problem(c, http.StatusBadRequest, "invalid_library_id", "libraryId is required", false)
		return
	}
	if _, _, err := s.retrievalScope(operationContext(c), []string{input.LibraryID}, nil); err != nil {
		s.problem(c, http.StatusForbidden, "retrieval_scope_denied", err.Error(), false)
		return
	}
	if s.Worker == nil || s.Worker.State() != "ok" {
		s.problem(c, http.StatusServiceUnavailable, "worker_unavailable", "Document worker is unavailable", true)
		return
	}
	var result map[string]any
	if err := s.Worker.RetrievalCancel(operationContext(c), input.LibraryID, &result); err != nil {
		s.problem(c, http.StatusBadRequest, "retrieval_cancel_failed", err.Error(), false)
		return
	}
	c.JSON(http.StatusOK, result)
}

func retrievalMinInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func retrievalMaxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

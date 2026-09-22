package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/worker"
	"github.com/gin-gonic/gin"
)

// allowedMCPProjects returns an explicit project grant. A library grant does
// not imply access to every project in that library. The returned set is
// revalidated against SQLite on every request so deleted projects and changed
// library membership cannot remain usable through a long-lived token.
func (s *Server) allowedMCPProjects(ctx context.Context, c *gin.Context, libraries, requested []string) ([]string, error) {
	value, _ := c.Get("identity")
	id, _ := value.(identity)
	requested = uniqueNonEmpty(requested)
	librarySet := map[string]bool{}
	for _, libraryID := range libraries {
		librarySet[libraryID] = true
	}
	granted := requested
	if !id.Desktop {
		if len(id.Token.ProjectIDs) == 0 {
			if len(requested) > 0 {
				return nil, errors.New("FORBIDDEN_SCOPE")
			}
			return []string{}, nil
		}
		if len(granted) == 0 {
			granted = append([]string{}, id.Token.ProjectIDs...)
		}
		allowedSet := map[string]bool{}
		for _, projectID := range id.Token.ProjectIDs {
			allowedSet[projectID] = true
		}
		for _, projectID := range granted {
			if !allowedSet[projectID] {
				return nil, errors.New("FORBIDDEN_SCOPE")
			}
		}
	}
	result := make([]string, 0, len(granted))
	seen := map[string]bool{}
	for _, projectID := range granted {
		if seen[projectID] {
			continue
		}
		project, err := s.Store.GetProject(ctx, projectID)
		if err != nil || !librarySet[project.LibraryID] {
			return nil, errors.New("FORBIDDEN_SCOPE")
		}
		seen[projectID] = true
		result = append(result, projectID)
	}
	return result, nil
}

func hasTokenScope(c *gin.Context, scope string) bool {
	value, _ := c.Get("identity")
	id, _ := value.(identity)
	if id.Desktop {
		return true
	}
	return contains(id.Token.Scopes, scope)
}

func (s *Server) requireEvidenceScope(c *gin.Context, scope string) error {
	if !hasTokenScope(c, "mcp_read") {
		return errors.New("mcp_read scope is required")
	}
	if !hasTokenScope(c, scope) {
		return fmt.Errorf("%s scope is required", scope)
	}
	return nil
}

func normalizeEvidenceSourceType(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "all"
	}
	switch value {
	case "raw", "architecture", "knowledge", "all":
		return value, nil
	default:
		return "", errors.New("sourceType must be raw, architecture, knowledge, or all")
	}
}

func normalizeEvidenceSourceTypes(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value, err := normalizeEvidenceSourceType(value)
		if err != nil || value == "all" {
			if err != nil {
				return nil, err
			}
			continue
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result, nil
}

// searchUnifiedRetrieval is the shared server-side retrieval path for MCP
// evidence. The worker ranks candidates, while the API rebuilds an
// authoritative corpus and verifies every returned ID/hash before exposing a
// citation. If the worker or embedding model is unavailable the same method
// performs explicit keyword-only fallback and marks the response degraded.
func (s *Server) searchUnifiedRetrieval(ctx context.Context, request model.RetrievalSearchRequest) (model.RetrievalSearchResponse, error) {
	if strings.TrimSpace(request.Query) == "" {
		return model.RetrievalSearchResponse{}, errors.New("query is required")
	}
	if request.TopK < 1 || request.TopK > 100 {
		request.TopK = 10
	}
	if request.RetrievalMode == "" {
		request.RetrievalMode = "hybrid"
	}
	libraries := uniqueNonEmpty(request.Filters.AllowedLibraryIDs)
	if len(libraries) == 0 {
		return model.RetrievalSearchResponse{Results: []model.RetrievalResult{}, Source: "empty", RetrievalMode: request.RetrievalMode, KeywordOnly: true, Degraded: true, DegradedReason: "No authorized libraries are available for evidence retrieval."}, nil
	}
	allowedLibraries := map[string]bool{}
	for _, id := range libraries {
		allowedLibraries[id] = true
	}
	projects := uniqueNonEmpty(request.Filters.AllowedProjectIDs)
	allowedProjects := map[string]bool{}
	for _, id := range projects {
		allowedProjects[id] = true
	}
	response := model.RetrievalSearchResponse{Results: []model.RetrievalResult{}, RetrievalMode: request.RetrievalMode}
	if s.Worker != nil && s.Worker.State() == "ok" {
		var workerResponse worker.RetrievalSearchResponse
		workerRequest := worker.RetrievalSearchRequest{Query: request.Query, TopK: request.TopK, RetrievalMode: request.RetrievalMode, Filters: worker.RetrievalFilters{
			SourceType: request.Filters.SourceType, SourceTypes: request.Filters.SourceTypes, ReviewStatuses: request.Filters.ReviewStatuses,
			LibraryIDs: libraries, AllowedLibraryIDs: libraries, ProjectIDs: request.Filters.ProjectIDs, AllowedProjectIDs: projects, AllowRaw: request.Filters.AllowRaw,
		}}
		if err := s.Worker.RetrievalSearch(ctx, workerRequest, &workerResponse); err == nil {
			response.Source, response.Generation = workerResponse.Source, workerResponse.Generation
			response.SemanticAvailable, response.KeywordOnly, response.Degraded = workerResponse.SemanticAvailable, workerResponse.KeywordOnly, workerResponse.Degraded
			response.DegradedReason, response.ModelID, response.ModelVersion, response.ConfigHash, response.Dimension = workerResponse.DegradedReason, workerResponse.ModelID, workerResponse.ModelVersion, workerResponse.ConfigHash, workerResponse.Dimension
			response.CorpusComplete, response.NeedsRebuild, response.RetrievalMode = workerResponse.CorpusComplete, workerResponse.NeedsRebuild, workerResponse.RetrievalMode
			corpus, err := s.Store.BuildRetrievalCorpus(ctx, libraries)
			if err != nil {
				return model.RetrievalSearchResponse{}, err
			}
			authoritative := make(map[string]model.RetrievalChunk, len(corpus))
			for _, item := range corpus {
				if retrievalFilterAllows(item, request.Filters, allowedLibraries, allowedProjects) {
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
			// An online worker may legitimately have no active generation (or a
			// generation marked incomplete while a rebuild is in progress). Keep
			// the authoritative SQLite keyword path as a floor so a missing or
			// stale index never hides current source evidence.
			if !response.KeywordOnly && !response.NeedsRebuild && len(response.Results) > 0 {
				return response, nil
			}
			return s.keywordRetrieval(ctx, request, response)
		}
		response.Degraded = true
		response.KeywordOnly = true
		response.SemanticAvailable = false
		response.DegradedReason = "Retrieval worker unavailable; authoritative SQLite keyword search was used."
		return s.keywordRetrieval(ctx, request, response)
	}
	return s.keywordRetrieval(ctx, request, response)
}

func (s *Server) keywordRetrieval(ctx context.Context, request model.RetrievalSearchRequest, response model.RetrievalSearchResponse) (model.RetrievalSearchResponse, error) {
	libraries := uniqueNonEmpty(request.Filters.AllowedLibraryIDs)
	if len(libraries) == 0 {
		return response, nil
	}
	allowedLibraries := map[string]bool{}
	for _, id := range libraries {
		allowedLibraries[id] = true
	}
	projects := uniqueNonEmpty(request.Filters.AllowedProjectIDs)
	allowedProjects := map[string]bool{}
	for _, id := range projects {
		allowedProjects[id] = true
	}
	corpus, err := s.Store.BuildRetrievalCorpus(ctx, libraries)
	if err != nil {
		return model.RetrievalSearchResponse{}, err
	}
	type ranked struct {
		item  model.RetrievalChunk
		score float64
	}
	terms := strings.Fields(strings.ToLower(request.Query))
	items := make([]ranked, 0)
	for _, item := range corpus {
		if !retrievalFilterAllows(item, request.Filters, allowedLibraries, allowedProjects) {
			continue
		}
		lower := strings.ToLower(item.Text)
		matches := 0
		for _, term := range terms {
			if term != "" && strings.Contains(lower, term) {
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
	existing := map[string]bool{}
	for _, item := range response.Results {
		existing[item.ID] = true
	}
	for _, value := range items[:retrievalMinInt(request.TopK, len(items))] {
		if existing[value.item.ID] {
			continue
		}
		response.Results = append(response.Results, model.RetrievalResult{RetrievalChunk: value.item, Scores: model.RetrievalScores{Lexical: value.score, Fusion: value.score, Final: value.score}})
		existing[value.item.ID] = true
		if len(response.Results) >= request.TopK {
			break
		}
	}
	if len(response.Results) == 0 || !response.SemanticAvailable {
		response.Source = "sqlite-keyword"
	} else {
		response.Source = "unified+sqlite-keyword"
	}
	if !response.SemanticAvailable {
		response.KeywordOnly = true
	}
	response.Degraded = true
	if response.DegradedReason == "" {
		response.DegradedReason = "Embedding index unavailable or incomplete; authoritative SQLite keyword search supplemented retrieval."
	}
	if !response.SemanticAvailable {
		response.RetrievalMode = "lexical"
	}
	return response, nil
}

type evidenceSearchInput struct {
	Query         string   `json:"query"`
	SourceType    string   `json:"sourceType"`
	SourceTypes   []string `json:"sourceTypes"`
	LibraryIDs    []string `json:"libraryIds"`
	ProjectIDs    []string `json:"projectIds"`
	TopK          int      `json:"topK"`
	RetrievalMode string   `json:"retrievalMode"`
}

func (s *Server) evidenceSearch(ctx context.Context, c *gin.Context, raw []byte) gin.H {
	if err := s.requireEvidenceScope(c, "evidence_search"); err != nil {
		return mcpFailure("FORBIDDEN_SCOPE", err.Error())
	}
	var input evidenceSearchInput
	if err := jsonUnmarshal(raw, &input); err != nil || strings.TrimSpace(input.Query) == "" {
		return mcpFailure("SCHEMA_INVALID", "query is required")
	}
	sourceType, err := normalizeEvidenceSourceType(input.SourceType)
	if err != nil {
		return mcpFailure("SCHEMA_INVALID", err.Error())
	}
	libraries, err := s.allowedMCPLibraries(c, input.LibraryIDs)
	if err != nil {
		return mcpFailure("FORBIDDEN_SCOPE", "requested library is outside token scope")
	}
	value, _ := c.Get("identity")
	id, _ := value.(identity)
	if id.Desktop && len(libraries) == 0 {
		items, listErr := s.Store.ListLibraries(ctx)
		if listErr != nil {
			return mcpFailure("DATABASE_ERROR", listErr.Error())
		}
		for _, item := range items {
			libraries = append(libraries, item.ID)
		}
	}
	projects, err := s.allowedMCPProjects(ctx, c, libraries, input.ProjectIDs)
	if err != nil {
		return mcpFailure("FORBIDDEN_SCOPE", "requested project is outside token scope")
	}
	sourceTypes, typesErr := normalizeEvidenceSourceTypes(input.SourceTypes)
	if typesErr != nil {
		return mcpFailure("SCHEMA_INVALID", typesErr.Error())
	}
	filters := model.RetrievalFilters{SourceType: sourceType, SourceTypes: sourceTypes, LibraryIDs: libraries, AllowedLibraryIDs: libraries, ProjectIDs: input.ProjectIDs, AllowedProjectIDs: projects, AllowRaw: sourceType == "raw" || sourceType == "all"}
	if (sourceType == "architecture" || contains(sourceTypes, "architecture")) && len(projects) == 0 {
		return mcpFailure("FORBIDDEN_SCOPE", "an explicit project grant is required for architecture evidence")
	}
	result, err := s.searchUnifiedRetrieval(ctx, model.RetrievalSearchRequest{Query: input.Query, Filters: filters, TopK: input.TopK, RetrievalMode: input.RetrievalMode})
	if err != nil {
		return mcpFailure("RETRIEVAL_ERROR", err.Error())
	}
	return gin.H{"content": []gin.H{{"type": "text", "text": fmt.Sprintf("%d evidence results returned.", len(result.Results))}}, "structuredContent": result}
}

func mcpFailure(code, message string) gin.H {
	return gin.H{"content": []gin.H{{"type": "text", "text": code + ": " + message}}, "isError": true, "structuredContent": gin.H{"code": code, "message": message}}
}

// jsonUnmarshal is kept as a small indirection so MCP argument decoding has a
// single bounded call site in this file and remains easy to fuzz in tests.
func jsonUnmarshal(raw []byte, target any) error {
	return json.Unmarshal(raw, target)
}

func (s *Server) evidenceRead(ctx context.Context, c *gin.Context, raw []byte) gin.H {
	if err := s.requireEvidenceScope(c, "evidence_read"); err != nil {
		return mcpFailure("FORBIDDEN_SCOPE", err.Error())
	}
	var input struct {
		ID        string `json:"id"`
		URI       string `json:"uri"`
		LibraryID string `json:"libraryId"`
		ProjectID string `json:"projectId"`
	}
	if err := jsonUnmarshal(raw, &input); err != nil {
		return mcpFailure("SCHEMA_INVALID", "id or uri is required")
	}
	identifier := strings.TrimSpace(input.ID)
	if identifier == "" {
		identifier = strings.TrimSpace(input.URI)
		if !validateEvidenceURI(identifier) {
			return mcpFailure("SCHEMA_INVALID", "uri must be a canonical kah:// evidence URI")
		}
	}
	if identifier == "" {
		return mcpFailure("SCHEMA_INVALID", "id or uri is required")
	}
	if strings.Contains(identifier, "://") && !validateEvidenceURI(identifier) {
		return mcpFailure("SCHEMA_INVALID", "uri must be a canonical kah:// evidence URI")
	}
	libraries, err := s.allowedMCPLibraries(c, []string{strings.TrimSpace(input.LibraryID)})
	if input.LibraryID == "" {
		libraries, err = s.allowedMCPLibraries(c, nil)
	}
	if err != nil || len(libraries) == 0 {
		return mcpFailure("FORBIDDEN_SCOPE", "library is outside token scope")
	}
	projects, err := s.allowedMCPProjects(ctx, c, libraries, []string{strings.TrimSpace(input.ProjectID)})
	if err != nil {
		return mcpFailure("FORBIDDEN_SCOPE", "project is outside token scope")
	}
	corpus, err := s.Store.BuildRetrievalCorpus(ctx, libraries)
	if err != nil {
		return mcpFailure("RETRIEVAL_ERROR", err.Error())
	}
	allowedLibraries := map[string]bool{}
	for _, libraryID := range libraries {
		allowedLibraries[libraryID] = true
	}
	allowedProjects := map[string]bool{}
	for _, projectID := range projects {
		allowedProjects[projectID] = true
	}
	var found *model.RetrievalChunk
	for index := range corpus {
		item := corpus[index]
		if item.ID != identifier && item.SourceURI != identifier {
			continue
		}
		filters := model.RetrievalFilters{SourceType: item.SourceType, AllowRaw: item.SourceType == "raw"}
		if retrievalFilterAllows(item, filters, allowedLibraries, allowedProjects) {
			copy := item
			found = &copy
			break
		}
	}
	if found == nil {
		return mcpFailure("EVIDENCE_NOT_FOUND", "evidence is unavailable, deleted, stale, or outside token scope")
	}
	return gin.H{"content": []gin.H{{"type": "resource", "resource": gin.H{"uri": found.SourceURI, "mimeType": "text/plain", "text": found.Text}}}, "structuredContent": model.EvidenceReadResponse{Result: model.RetrievalResult{RetrievalChunk: *found}}}
}

// Keep net/url and storage imports part of the authoritative URI validation
// boundary. This helper rejects URI forms that could otherwise be confused
// with a filesystem path by a future evidence reader.
func validateEvidenceURI(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "kah" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || strings.Contains(value, "..") || strings.ContainsAny(parsed.Path, `\\`) {
		return false
	}
	switch parsed.Host {
	case "document", "knowledge", "architecture":
		return parsed.Path != "" && !strings.HasPrefix(parsed.Path, "//")
	default:
		return false
	}
}

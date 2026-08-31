package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/gin-gonic/gin"
)

const mcpProtocolVersion = "2025-11-25"

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string       `json:"jsonrpc"`
	ID      any          `json:"id"`
	Result  any          `json:"result,omitempty"`
	Error   *mcpRPCError `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) mcpOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if !isAllowedMCPOrigin(origin) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "MCP Origin is not allowed"})
			return
		}
		c.Next()
	}
}

func isAllowedMCPOrigin(origin string) bool {
	if origin == "" || origin == "null" || origin == "file://" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) handleMCP(mode string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
		var request mcpRequest
		if err := c.ShouldBindJSON(&request); err != nil || request.JSONRPC != "2.0" || request.Method == "" {
			c.JSON(http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", ID: nil, Error: &mcpRPCError{Code: -32600, Message: "Invalid Request"}})
			return
		}
		var id any
		if len(request.ID) > 0 && string(request.ID) != "null" {
			if err := json.Unmarshal(request.ID, &id); err != nil {
				c.JSON(http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", ID: nil, Error: &mcpRPCError{Code: -32600, Message: "Invalid Request"}})
				return
			}
		}
		result, rpcErr := s.dispatchMCP(operationContext(c), c, mode, request)
		if request.Method == "initialize" && rpcErr == nil {
			if initialized, ok := result.(gin.H); ok {
				if version, ok := initialized["protocolVersion"].(string); ok {
					c.Header("MCP-Protocol-Version", version)
				}
			}
		}
		if id == nil {
			c.Status(http.StatusAccepted)
			return
		}
		if rpcErr != nil {
			c.JSON(http.StatusOK, mcpResponse{JSONRPC: "2.0", ID: id, Error: rpcErr})
			return
		}
		c.JSON(http.StatusOK, mcpResponse{JSONRPC: "2.0", ID: id, Result: result})
	}
}

func (s *Server) dispatchMCP(ctx context.Context, c *gin.Context, mode string, request mcpRequest) (any, *mcpRPCError) {
	switch request.Method {
	case "initialize":
		version := mcpProtocolVersion
		var input struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(request.Params) > 0 && json.Unmarshal(request.Params, &input) == nil {
			switch input.ProtocolVersion {
			case "2025-03-26", "2025-06-18", mcpProtocolVersion:
				version = input.ProtocolVersion
			}
		}
		return gin.H{"protocolVersion": version, "serverInfo": gin.H{"name": "kah-knowledge-" + mode, "version": s.Config.Version}, "capabilities": gin.H{"tools": gin.H{"listChanged": false}, "resources": gin.H{"subscribe": false, "listChanged": false}}}, nil
	case "notifications/initialized":
		return gin.H{}, nil
	case "ping":
		return gin.H{}, nil
	case "tools/list":
		return gin.H{"tools": mcpTools(mode)}, nil
	case "resources/list":
		return s.mcpListResources(ctx, c, mode)
	case "resources/templates/list":
		return gin.H{"resourceTemplates": []gin.H{
			{"uriTemplate": "kah://knowledge/{knowledgeId}", "name": "KAH knowledge revision", "description": "A KAH knowledge resource; add ?revision=N for a historical published revision and #section-id for one section.", "mimeType": "text/markdown"},
			{"uriTemplate": "kah://document/{documentId}", "name": "Imported source document", "description": "An indexed imported document in the token-scoped library; use its content as traceable source material.", "mimeType": "text/markdown"},
		}}, nil
	case "resources/read":
		var input struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(request.Params, &input); err != nil || input.URI == "" {
			return nil, &mcpRPCError{Code: -32602, Message: "uri is required"}
		}
		result, err := s.mcpReadResource(ctx, c, mode, input.URI)
		if err != nil {
			return nil, &mcpRPCError{Code: -32004, Message: err.Error()}
		}
		return result, nil
	case "tools/call":
		var input struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &input); err != nil || input.Name == "" {
			return nil, &mcpRPCError{Code: -32602, Message: "tool name is required"}
		}
		return s.callMCPTool(ctx, c, mode, input.Name, input.Arguments), nil
	default:
		return nil, &mcpRPCError{Code: -32601, Message: "Method not found"}
	}
}

func mcpTools(mode string) []gin.H {
	searchSchema := gin.H{"type": "object", "required": []string{"query"}, "properties": gin.H{"query": gin.H{"type": "string"}, "libraryIds": gin.H{"type": "array", "items": gin.H{"type": "string"}}, "types": gin.H{"type": "array", "items": gin.H{"type": "string"}}, "language": gin.H{"type": "string"}, "classifications": gin.H{"type": "object", "additionalProperties": gin.H{"type": "array", "items": gin.H{"type": "string"}}}, "tags": gin.H{"type": "array", "items": gin.H{"type": "string"}}, "statuses": gin.H{"type": "array", "items": gin.H{"type": "string", "enum": []string{"stable"}}}, "limit": gin.H{"type": "integer", "minimum": 1, "maximum": 100}, "cursor": gin.H{"type": "string"}}}
	getSchema := gin.H{"type": "object", "required": []string{"uri"}, "properties": gin.H{"uri": gin.H{"type": "string"}, "sectionIds": gin.H{"type": "array", "items": gin.H{"type": "string"}}, "includeSources": gin.H{"type": "boolean"}, "includeRelations": gin.H{"type": "boolean"}}}
	tools := []gin.H{
		{"name": "knowledge_search", "description": "Search KAH knowledge and return a compact directory. Read entries before requesting full knowledge.", "inputSchema": searchSchema, "annotations": gin.H{"readOnlyHint": true}},
		{"name": "knowledge_get", "description": "Read one stable KAH knowledge revision or selected stable section IDs.", "inputSchema": getSchema, "annotations": gin.H{"readOnlyHint": true}},
	}
	if mode == "manage" {
		section := gin.H{"type": "object", "required": []string{"id", "heading", "content"}, "properties": gin.H{
			"id": gin.H{"type": "string"}, "heading": gin.H{"type": "string"}, "content": gin.H{"type": "string"},
		}}
		source := gin.H{"type": "object", "required": []string{"id", "resource"}, "properties": gin.H{
			"id": gin.H{"type": "string"}, "resource": gin.H{"type": "string"}, "title": gin.H{"type": "string"},
			"locator": gin.H{"type": "object", "additionalProperties": true},
			"snapshot": gin.H{"type": "object", "properties": gin.H{
				"status": gin.H{"type": "string"}, "content_hash": gin.H{"type": "string"}, "captured_at": gin.H{"type": "string"},
			}},
		}}
		candidate := gin.H{"type": "object", "description": "KAH Knowledge Profile v1 JSON candidate. Some clients may encode this nested object as a JSON string; the server accepts both forms.", "required": []string{"schema", "type", "title", "description", "language", "sections", "sources"}, "properties": gin.H{
			"schema": gin.H{"type": "string", "description": "Must be kah-knowledge/v1"},
			"id":     gin.H{"type": "string"}, "revision": gin.H{"type": "integer"},
			"type":    gin.H{"type": "string", "enum": []string{"concept", "claim", "procedure", "decision", "policy", "reference"}},
			"subtype": gin.H{"type": "string"}, "title": gin.H{"type": "string"}, "description": gin.H{"type": "string"},
			"language":        gin.H{"type": "string", "enum": []string{"zh-CN", "en"}},
			"aliases":         gin.H{"type": "array", "items": gin.H{"type": "string"}},
			"primary_path":    gin.H{"type": "array", "items": gin.H{"type": "string"}},
			"classifications": gin.H{"type": "object", "additionalProperties": gin.H{"type": "array", "items": gin.H{"type": "string"}}},
			"tags":            gin.H{"type": "array", "items": gin.H{"type": "string"}},
			"sections":        gin.H{"type": "array", "items": section}, "sources": gin.H{"type": "array", "items": source},
			"duplicate_intent": gin.H{"type": "string"},
		}}
		tools = append(tools,
			gin.H{"name": "knowledge_validate", "description": "Validate a KAH v1 candidate before submitting it.", "inputSchema": gin.H{"type": "object", "required": []string{"libraryId", "candidate"}, "properties": gin.H{"libraryId": gin.H{"type": "string"}, "candidate": candidate}}, "annotations": gin.H{"readOnlyHint": true}},
			gin.H{"name": "knowledge_submit", "description": "Create a review-gated draft or propose an immutable revision. It does not approve or publish knowledge.", "inputSchema": gin.H{"type": "object", "required": []string{"libraryId", "mode", "candidate", "idempotencyKey"}, "properties": gin.H{"libraryId": gin.H{"type": "string"}, "mode": gin.H{"enum": []string{"create", "propose_revision"}}, "baseUri": gin.H{"type": "string"}, "candidate": candidate, "idempotencyKey": gin.H{"type": "string"}}}, "annotations": gin.H{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true}},
			gin.H{"name": "knowledge_submission_list", "description": "List knowledge submissions in the token-scoped libraries so an Agent can manage the review queue.", "inputSchema": gin.H{"type": "object", "properties": gin.H{"libraryIds": gin.H{"type": "array", "items": gin.H{"type": "string"}}, "statuses": gin.H{"type": "array", "items": gin.H{"type": "string", "enum": []string{"pending_review", "reviewing", "approved_pending_index", "published", "rejected"}}}, "limit": gin.H{"type": "integer", "minimum": 1, "maximum": 100}}}, "annotations": gin.H{"readOnlyHint": true}},
			gin.H{"name": "knowledge_submission_get", "description": "Read the candidate, provenance, validation, review history, and publication status of a submission.", "inputSchema": gin.H{"type": "object", "required": []string{"submissionId"}, "properties": gin.H{"submissionId": gin.H{"type": "string"}}}, "annotations": gin.H{"readOnlyHint": true}},
			gin.H{"name": "knowledge_compare", "description": "Compare a submitted candidate with a stable revision, another submission, or its previous revision.", "inputSchema": gin.H{"type": "object", "required": []string{"submissionId"}, "properties": gin.H{"submissionId": gin.H{"type": "string"}, "baseUri": gin.H{"type": "string", "description": "Optional KAH URI to use as the comparison baseline."}, "baseSubmissionId": gin.H{"type": "string", "description": "Optional submission to use as the comparison baseline."}}}, "annotations": gin.H{"readOnlyHint": true}},
			gin.H{"name": "knowledge_review", "description": "Record an Agent review. Approval is published only when confidence is strictly greater than 0.95; lower confidence remains pending human review.", "inputSchema": gin.H{"type": "object", "required": []string{"submissionId", "decision", "confidence"}, "properties": gin.H{"submissionId": gin.H{"type": "string"}, "decision": gin.H{"type": "string", "enum": []string{"approve", "reject", "needs_human"}}, "confidence": gin.H{"type": "number", "minimum": 0, "maximum": 1, "description": "Evidence-backed confidence in the 0..1 range. Agent approval requires > 0.95."}, "reason": gin.H{"type": "string"}}}, "annotations": gin.H{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false}},
		)
	}
	return tools
}

type mcpKnowledgeToolInput struct {
	LibraryID      string                 `json:"libraryId"`
	Mode           string                 `json:"mode"`
	BaseURI        string                 `json:"baseUri"`
	CandidateRaw   json.RawMessage        `json:"candidate"`
	IdempotencyKey string                 `json:"idempotencyKey"`
	Candidate      model.KnowledgePayload `json:"-"`
}

type mcpSubmissionListInput struct {
	LibraryIDs []string `json:"libraryIds"`
	Statuses   []string `json:"statuses"`
	Limit      int      `json:"limit"`
}

type mcpSubmissionCompareInput struct {
	SubmissionID     string `json:"submissionId"`
	BaseURI          string `json:"baseUri"`
	BaseSubmissionID string `json:"baseSubmissionId"`
}

type mcpSubmissionReviewInput struct {
	SubmissionID string   `json:"submissionId"`
	Decision     string   `json:"decision"`
	Confidence   *float64 `json:"confidence"`
	Reason       string   `json:"reason"`
}

func decodeMCPKnowledgeToolInput(raw json.RawMessage) (mcpKnowledgeToolInput, error) {
	var input mcpKnowledgeToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, err
	}
	if input.LibraryID == "" && len(input.CandidateRaw) == 0 {
		var wrapper struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper.Input) > 0 {
			var nested mcpKnowledgeToolInput
			if err := json.Unmarshal(wrapper.Input, &nested); err == nil {
				input = nested
			}
		}
	}
	if len(input.CandidateRaw) == 0 || string(input.CandidateRaw) == "null" {
		return input, errors.New("candidate is required")
	}
	if err := json.Unmarshal(input.CandidateRaw, &input.Candidate); err == nil {
		return input, nil
	}
	var encoded string
	if err := json.Unmarshal(input.CandidateRaw, &encoded); err != nil {
		return input, err
	}
	if err := json.Unmarshal([]byte(encoded), &input.Candidate); err != nil {
		return input, err
	}
	return input, nil
}

func mcpResources(mode string) []gin.H {
	resources := []gin.H{
		{"uri": "kah://schema/kah-knowledge/v1", "name": "KAH Knowledge Profile v1", "mimeType": "application/json"},
		{"uri": "kah://skill/read/v1", "name": "KAH MCP Read Skill", "mimeType": "text/markdown"},
	}
	if mode == "manage" {
		resources = append(resources, gin.H{"uri": "kah://skill/manage/v1", "name": "KAH MCP Manage Skill", "mimeType": "text/markdown"})
	}
	return resources
}

func (s *Server) mcpListResources(ctx context.Context, c *gin.Context, mode string) (gin.H, *mcpRPCError) {
	resources := mcpResources(mode)
	libraries, err := s.allowedMCPLibraries(c, nil)
	if err != nil {
		return nil, &mcpRPCError{Code: -32003, Message: "requested library is outside token scope"}
	}
	value, _ := c.Get("identity")
	id, _ := value.(identity)
	documents := []model.Document{}
	if id.Desktop || len(libraries) == 0 {
		documents, err = s.Store.ListDocuments(ctx, "")
	} else {
		for _, libraryID := range libraries {
			items, listErr := s.Store.ListDocuments(ctx, libraryID)
			if listErr != nil {
				err = listErr
				break
			}
			documents = append(documents, items...)
		}
	}
	if err != nil {
		return nil, &mcpRPCError{Code: -32000, Message: err.Error()}
	}
	for _, document := range documents {
		if document.Status != "ready" {
			continue
		}
		resources = append(resources, gin.H{
			"uri":         storage.DocumentURI(document.ID),
			"name":        document.Title,
			"description": "Imported source document (content hash " + document.ContentHash + ")",
			"mimeType":    documentMediaType(document),
		})
	}
	return gin.H{"resources": resources}, nil
}

func (s *Server) allowedMCPLibraries(c *gin.Context, requested []string) ([]string, error) {
	value, _ := c.Get("identity")
	id, _ := value.(identity)
	normalized := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, libraryID := range requested {
		libraryID = strings.TrimSpace(libraryID)
		if libraryID == "" {
			return nil, errors.New("FORBIDDEN_SCOPE")
		}
		if !seen[libraryID] {
			normalized = append(normalized, libraryID)
			seen[libraryID] = true
		}
	}
	if id.Desktop {
		return normalized, nil
	}
	allowed := id.Token.LibraryIDs
	if len(normalized) == 0 {
		return append([]string{}, allowed...), nil
	}
	for _, libraryID := range normalized {
		if !contains(allowed, libraryID) {
			return nil, errors.New("FORBIDDEN_SCOPE")
		}
	}
	return normalized, nil
}

func (s *Server) callMCPTool(ctx context.Context, c *gin.Context, mode, name string, raw json.RawMessage) gin.H {
	fail := func(code, message string) gin.H {
		return gin.H{"content": []gin.H{{"type": "text", "text": code + ": " + message}}, "isError": true, "structuredContent": gin.H{"code": code, "message": message}}
	}
	switch name {
	case "knowledge_search":
		var request model.KnowledgeSearchRequest
		if err := json.Unmarshal(raw, &request); err != nil || strings.TrimSpace(request.Query) == "" {
			return fail("SCHEMA_INVALID", "query is required")
		}
		libraries, err := s.allowedMCPLibraries(c, request.LibraryIDs)
		if err != nil {
			return fail("FORBIDDEN_SCOPE", "requested library is outside token scope")
		}
		request.LibraryIDs = libraries
		result, err := s.Store.SearchKnowledge(ctx, request)
		if err != nil {
			return fail("DATABASE_ERROR", err.Error())
		}
		links := []gin.H{}
		for _, item := range result.Results {
			links = append(links, gin.H{"type": "resource_link", "uri": item.URI + "?revision=" + fmt.Sprint(item.Revision), "name": item.Title, "description": item.Description, "mimeType": "text/markdown"})
		}
		content := []gin.H{{"type": "text", "text": "Knowledge directory returned. Select entries and call knowledge_get or resources/read."}}
		content = append(content, links...)
		return gin.H{"content": content, "structuredContent": result}
	case "knowledge_get":
		var input struct {
			URI              string   `json:"uri"`
			SectionIDs       []string `json:"sectionIds"`
			IncludeSources   bool     `json:"includeSources"`
			IncludeRelations bool     `json:"includeRelations"`
		}
		if err := json.Unmarshal(raw, &input); err != nil || input.URI == "" {
			return fail("SCHEMA_INVALID", "uri is required")
		}
		item, err := s.Store.GetKnowledge(ctx, input.URI, mode == "manage")
		if err != nil {
			return fail("KNOWLEDGE_NOT_FOUND", err.Error())
		}
		libraries, scopeErr := s.allowedMCPLibraries(c, []string{item.LibraryID})
		if scopeErr != nil || len(libraries) != 1 {
			return fail("FORBIDDEN_SCOPE", "knowledge is outside token scope")
		}
		if len(input.SectionIDs) > 0 {
			sections, missing := selectSections(item.Payload.Sections, input.SectionIDs)
			if len(missing) > 0 {
				return fail("SECTION_NOT_FOUND", "unknown section: "+strings.Join(missing, ", "))
			}
			item.Payload.Sections = sections
		}
		if mode == "manage" && !mcpDraftVisible(c, item) {
			return fail("FORBIDDEN_SCOPE", "draft is not owned by this token")
		}
		if !input.IncludeSources {
			item.Payload.Sources = nil
		}
		if !input.IncludeRelations {
			item.Payload.Relations = nil
		}
		if mode == "read" {
			item.Payload.Derivation = derivationSummary(item.Payload.Derivation)
		}
		return gin.H{"content": []gin.H{{"type": "resource", "resource": gin.H{"uri": item.URI + "?revision=" + fmt.Sprint(item.Revision), "mimeType": "application/json", "text": mustJSONString(item)}}}, "structuredContent": item}
	case "knowledge_validate":
		if mode != "manage" {
			return fail("FORBIDDEN_SCOPE", "manage access is required")
		}
		input, err := decodeMCPKnowledgeToolInput(raw)
		if err != nil || strings.TrimSpace(input.LibraryID) == "" {
			return fail("SCHEMA_INVALID", "libraryId and candidate are required")
		}
		if _, err := s.allowedMCPLibraries(c, []string{input.LibraryID}); err != nil {
			return fail("FORBIDDEN_SCOPE", "library is outside token scope")
		}
		result := storage.ValidateKnowledgePayload(input.Candidate, true)
		if result.Valid {
			for _, referenceIssue := range s.validateMCPKnowledgeReferences(ctx, c, result.Normalized) {
				result.Errors = append(result.Errors, model.KnowledgeValidationIssue{Code: "SOURCE_INVALID", Path: "sources", Message: referenceIssue})
			}
			result.Valid = len(result.Errors) == 0
		}
		return gin.H{"content": []gin.H{{"type": "text", "text": validationMessage(result)}}, "structuredContent": result, "isError": !result.Valid}
	case "knowledge_submit":
		if mode != "manage" {
			return fail("FORBIDDEN_SCOPE", "manage access is required")
		}
		input, err := decodeMCPKnowledgeToolInput(raw)
		if err != nil {
			return fail("SCHEMA_INVALID", "libraryId, candidate, and idempotencyKey are required")
		}
		input.LibraryID = strings.TrimSpace(input.LibraryID)
		input.Mode = strings.TrimSpace(input.Mode)
		input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
		if input.LibraryID == "" || input.IdempotencyKey == "" {
			return fail("SCHEMA_INVALID", "libraryId, candidate, and idempotencyKey are required")
		}
		if input.Mode != "create" && input.Mode != "propose_revision" {
			return fail("SCHEMA_INVALID", "mode must be create or propose_revision")
		}
		if input.Mode == "propose_revision" && strings.TrimSpace(input.BaseURI) == "" {
			return fail("SCHEMA_INVALID", "baseUri is required for propose_revision")
		}
		if _, err := s.allowedMCPLibraries(c, []string{input.LibraryID}); err != nil {
			return fail("FORBIDDEN_SCOPE", "library is outside token scope")
		}
		if referenceErrors := s.validateMCPKnowledgeReferences(ctx, c, input.Candidate); len(referenceErrors) > 0 {
			return fail("SCHEMA_INVALID", strings.Join(referenceErrors, "; "))
		}
		s.captureHTTPSnapshots(ctx, &input.Candidate)
		value, _ := c.Get("identity")
		caller, _ := value.(identity)
		submission, duplicate, err := s.Store.CreateKnowledgeDraft(ctx, storage.KnowledgeDraftInput{LibraryID: input.LibraryID, TokenID: caller.Token.ID, ClientSubmissionID: input.IdempotencyKey, Mode: input.Mode, BaseURI: input.BaseURI, Payload: input.Candidate, RequireSources: true})
		if err != nil {
			code := "SCHEMA_INVALID"
			if err.Error() == "EXACT_DUPLICATE" {
				code = "EXACT_DUPLICATE"
			}
			if err.Error() == "NEAR_DUPLICATE_REQUIRES_INTENT" {
				code = "NEAR_DUPLICATE_REQUIRES_INTENT"
			}
			return gin.H{"content": []gin.H{{"type": "text", "text": code + ": " + err.Error()}}, "isError": true, "structuredContent": submission}
		}
		_ = s.Store.AddAudit(ctx, caller.Token.ID, "kah_mcp_submission_created", submission.KnowledgeURI, map[string]any{"submissionId": submission.ID, "duplicateRequest": duplicate})
		message := "Draft submitted for human review; it is not searchable until published."
		if duplicate {
			message = "Idempotent replay; the existing draft was returned."
		}
		return gin.H{"content": []gin.H{{"type": "text", "text": message}}, "structuredContent": submission}
	case "knowledge_submission_list":
		if mode != "manage" {
			return fail("FORBIDDEN_SCOPE", "manage access is required")
		}
		var input mcpSubmissionListInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return fail("SCHEMA_INVALID", "invalid submission list arguments")
		}
		libraries, err := s.allowedMCPLibraries(c, input.LibraryIDs)
		if err != nil {
			return fail("FORBIDDEN_SCOPE", "requested library is outside token scope")
		}
		statusFilter := map[string]bool{}
		for _, status := range input.Statuses {
			status = strings.TrimSpace(status)
			if status != "pending_review" && status != "reviewing" && status != "approved_pending_index" && status != "published" && status != "rejected" {
				return fail("SCHEMA_INVALID", "unsupported submission status: "+status)
			}
			statusFilter[status] = true
		}
		limit := input.Limit
		if limit == 0 {
			limit = 50
		}
		if limit < 1 || limit > 100 {
			return fail("SCHEMA_INVALID", "limit must be between 1 and 100")
		}
		all := []model.KAHSubmissionDirectoryEntry{}
		appendLibrary := func(libraryID string) error {
			items, listErr := s.Store.ListKAHSubmissions(ctx, libraryID)
			if listErr != nil {
				return listErr
			}
			for _, item := range items {
				if len(statusFilter) > 0 && !statusFilter[item.ReviewStatus] {
					continue
				}
				all = append(all, kahSubmissionDirectoryEntry(item))
			}
			return nil
		}
		if len(libraries) == 0 {
			if err := appendLibrary(""); err != nil {
				return fail("DATABASE_ERROR", err.Error())
			}
		} else {
			for _, libraryID := range libraries {
				if err := appendLibrary(libraryID); err != nil {
					return fail("DATABASE_ERROR", err.Error())
				}
			}
		}
		sort.SliceStable(all, func(i, j int) bool { return all[i].UpdatedAt.After(all[j].UpdatedAt) })
		if len(all) > limit {
			all = all[:limit]
		}
		result := model.KAHSubmissionListResponse{Submissions: all}
		return gin.H{"content": []gin.H{{"type": "text", "text": fmt.Sprintf("%d knowledge submissions returned.", len(all))}}, "structuredContent": result}
	case "knowledge_submission_get":
		if mode != "manage" {
			return fail("FORBIDDEN_SCOPE", "manage access is required")
		}
		var input struct {
			SubmissionID string `json:"submissionId"`
		}
		if err := json.Unmarshal(raw, &input); err != nil || input.SubmissionID == "" {
			return fail("SCHEMA_INVALID", "submissionId is required")
		}
		item, err := s.Store.GetKAHSubmission(ctx, input.SubmissionID)
		if err != nil {
			return fail("KNOWLEDGE_NOT_FOUND", err.Error())
		}
		if _, err := s.allowedMCPLibraries(c, []string{item.LibraryID}); err != nil {
			return fail("FORBIDDEN_SCOPE", "submission is outside token scope")
		}
		return gin.H{"content": []gin.H{{"type": "text", "text": "Submission status: " + item.ReviewStatus}}, "structuredContent": item}
	case "knowledge_compare":
		if mode != "manage" {
			return fail("FORBIDDEN_SCOPE", "manage access is required")
		}
		var input mcpSubmissionCompareInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return fail("SCHEMA_INVALID", "submissionId is required")
		}
		input.SubmissionID = strings.TrimSpace(input.SubmissionID)
		input.BaseURI = strings.TrimSpace(input.BaseURI)
		input.BaseSubmissionID = strings.TrimSpace(input.BaseSubmissionID)
		if input.SubmissionID == "" {
			return fail("SCHEMA_INVALID", "submissionId is required")
		}
		if input.BaseURI != "" && input.BaseSubmissionID != "" {
			return fail("SCHEMA_INVALID", "baseUri and baseSubmissionId are mutually exclusive")
		}
		submission, err := s.Store.GetKAHSubmission(ctx, input.SubmissionID)
		if err != nil {
			return fail("KNOWLEDGE_NOT_FOUND", err.Error())
		}
		if _, err := s.allowedMCPLibraries(c, []string{submission.LibraryID}); err != nil {
			return fail("FORBIDDEN_SCOPE", "submission is outside token scope")
		}
		candidate, err := s.Store.GetKnowledge(ctx, submission.KnowledgeURI+"?revision="+fmt.Sprint(submission.Revision), true)
		if err != nil {
			return fail("KNOWLEDGE_NOT_FOUND", err.Error())
		}
		var base *model.KnowledgeRevision
		if input.BaseSubmissionID != "" {
			if input.BaseSubmissionID == submission.ID {
				return fail("SCHEMA_INVALID", "baseSubmissionId must differ from submissionId")
			}
			baseSubmission, baseErr := s.Store.GetKAHSubmission(ctx, input.BaseSubmissionID)
			if baseErr != nil {
				return fail("KNOWLEDGE_NOT_FOUND", baseErr.Error())
			}
			if baseSubmission.LibraryID != submission.LibraryID {
				return fail("FORBIDDEN_SCOPE", "comparison baseline is outside the submission library")
			}
			baseRevision, baseErr := s.Store.GetKnowledge(ctx, baseSubmission.KnowledgeURI+"?revision="+fmt.Sprint(baseSubmission.Revision), true)
			if baseErr != nil {
				return fail("KNOWLEDGE_NOT_FOUND", baseErr.Error())
			}
			base = &baseRevision
		} else if input.BaseURI != "" {
			baseRevision, baseErr := s.Store.GetKnowledge(ctx, input.BaseURI, true)
			if baseErr != nil {
				return fail("KNOWLEDGE_NOT_FOUND", baseErr.Error())
			}
			if _, baseErr = s.allowedMCPLibraries(c, []string{baseRevision.LibraryID}); baseErr != nil || baseRevision.LibraryID != submission.LibraryID {
				return fail("FORBIDDEN_SCOPE", "comparison baseline is outside the submission library")
			}
			base = &baseRevision
		} else if submission.Revision > 1 {
			previous, previousErr := s.Store.GetKnowledge(ctx, submission.KnowledgeURI+"?revision="+fmt.Sprint(submission.Revision-1), true)
			if previousErr == nil {
				base = &previous
			}
		}
		comparison := compareKAHKnowledge(submission.ID, candidate, base)
		return gin.H{"content": []gin.H{{"type": "text", "text": comparisonMessage(comparison)}}, "structuredContent": comparison}
	case "knowledge_review":
		if mode != "manage" {
			return fail("FORBIDDEN_SCOPE", "manage access is required")
		}
		var input mcpSubmissionReviewInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return fail("SCHEMA_INVALID", "submissionId, decision, and confidence are required")
		}
		input.SubmissionID = strings.TrimSpace(input.SubmissionID)
		input.Decision = strings.TrimSpace(input.Decision)
		input.Reason = strings.TrimSpace(input.Reason)
		if input.SubmissionID == "" || input.Decision == "" || input.Confidence == nil {
			return fail("SCHEMA_INVALID", "submissionId, decision, and confidence are required")
		}
		confidence := *input.Confidence
		if confidence < 0 || confidence > 1 {
			return fail("SCHEMA_INVALID", "confidence must be between 0 and 1")
		}
		if input.Decision != "approve" && input.Decision != "reject" && input.Decision != "needs_human" {
			return fail("SCHEMA_INVALID", "decision must be approve, reject, or needs_human")
		}
		submission, err := s.Store.GetKAHSubmission(ctx, input.SubmissionID)
		if err != nil {
			return fail("KNOWLEDGE_NOT_FOUND", err.Error())
		}
		if _, err := s.allowedMCPLibraries(c, []string{submission.LibraryID}); err != nil {
			return fail("FORBIDDEN_SCOPE", "submission is outside token scope")
		}
		revision, err := s.Store.GetKnowledge(ctx, submission.KnowledgeURI+"?revision="+fmt.Sprint(submission.Revision), true)
		if err != nil {
			return fail("KNOWLEDGE_NOT_FOUND", err.Error())
		}
		effectiveDecision := input.Decision
		reason := input.Reason
		if effectiveDecision == "approve" && confidence <= storage.KAHAgentApprovalConfidenceThreshold {
			effectiveDecision = "needs_human"
			if reason == "" {
				reason = fmt.Sprintf("Agent confidence %.2f does not exceed the %.2f approval threshold.", confidence, storage.KAHAgentApprovalConfidenceThreshold)
			}
		}
		if effectiveDecision == "approve" && !submission.Validation.Valid {
			effectiveDecision = "needs_human"
			if reason == "" {
				reason = "Submission validation is not valid; human review is required."
			}
		}
		if effectiveDecision == "approve" && contains(revision.Flags, "source-unverified") {
			effectiveDecision = "needs_human"
			if reason == "" {
				reason = "One or more sources are unverified; human review is required."
			}
		}
		if (effectiveDecision == "reject" || effectiveDecision == "needs_human") && reason == "" {
			return fail("SCHEMA_INVALID", "reason is required for reject or needs_human")
		}
		reviewed, err := s.Store.ReviewKAHSubmissionWithType(ctx, submission.ID, "agent", actorName(c), effectiveDecision, reason, confidence)
		if err != nil {
			return fail("REVIEW_FAILED", err.Error())
		}
		result := model.KAHAgentReviewResult{
			Submission:          reviewed,
			RequestedDecision:   input.Decision,
			Decision:            effectiveDecision,
			ReviewerType:        "agent",
			Confidence:          confidence,
			ConfidenceThreshold: storage.KAHAgentApprovalConfidenceThreshold,
			ApprovalEligible:    effectiveDecision == "approve",
			RequiresHumanReview: effectiveDecision == "needs_human",
		}
		_ = s.Store.AddAudit(ctx, actorName(c), "kah_mcp_review_recorded", reviewed.KnowledgeURI, map[string]any{
			"submissionId": reviewed.ID, "requestedDecision": input.Decision, "decision": effectiveDecision,
			"confidence": confidence, "confidenceThreshold": storage.KAHAgentApprovalConfidenceThreshold,
		})
		message := "Agent review recorded: " + effectiveDecision + "."
		if effectiveDecision == "approve" {
			published, publishErr := s.Store.PublishKAHSubmission(ctx, reviewed.ID)
			if publishErr != nil {
				result.Submission = reviewed
				return gin.H{"content": []gin.H{{"type": "text", "text": "PUBLISH_FAILED: " + publishErr.Error()}}, "isError": true, "structuredContent": result}
			}
			result.Published = true
			result.PublishedKnowledge = &published
			result.Submission, _ = s.Store.GetKAHSubmission(ctx, reviewed.ID)
			_ = s.Store.AddAudit(ctx, actorName(c), "kah_mcp_knowledge_published", published.URI, map[string]any{"revision": published.Revision, "submissionId": reviewed.ID})
			message = "Agent review approved and published the knowledge because confidence exceeded the threshold."
		}
		return gin.H{"content": []gin.H{{"type": "text", "text": message}}, "structuredContent": result}
	default:
		return fail("METHOD_NOT_FOUND", "unknown or unavailable tool")
	}
}

func (s *Server) mcpReadResource(ctx context.Context, c *gin.Context, mode, uri string) (gin.H, error) {
	switch uri {
	case "kah://schema/kah-knowledge/v1":
		return gin.H{"contents": []gin.H{{"uri": uri, "mimeType": "application/json", "text": kahSchemaReference}}}, nil
	case "kah://skill/read/v1":
		return gin.H{"contents": []gin.H{{"uri": uri, "mimeType": "text/markdown", "text": kahReadSkill}}}, nil
	case "kah://skill/manage/v1":
		if mode == "manage" {
			return gin.H{"contents": []gin.H{{"uri": uri, "mimeType": "text/markdown", "text": kahManageSkill}}}, nil
		}
		return nil, errors.New("resource not available")
	}
	if strings.HasPrefix(uri, "kah://document/") {
		documentID, err := storage.ParseDocumentURI(uri)
		if err != nil {
			return nil, err
		}
		detail, err := s.Store.GetDocument(ctx, documentID)
		if err != nil {
			return nil, err
		}
		if _, err := s.allowedMCPLibraries(c, []string{detail.LibraryID}); err != nil {
			return nil, errors.New("FORBIDDEN_SCOPE")
		}
		if detail.Status != "ready" {
			return nil, errors.New("document is not ready")
		}
		text := documentResourceText(detail)
		if text == "" {
			return nil, errors.New("document has no indexed text")
		}
		return gin.H{"contents": []gin.H{{"uri": uri, "mimeType": documentMediaType(detail.Document), "text": text}}}, nil
	}
	item, err := s.Store.GetKnowledge(ctx, uri, mode == "manage")
	if err != nil {
		return nil, err
	}
	if _, err := s.allowedMCPLibraries(c, []string{item.LibraryID}); err != nil {
		return nil, errors.New("FORBIDDEN_SCOPE")
	}
	if mode == "manage" && !mcpDraftVisible(c, item) {
		return nil, errors.New("FORBIDDEN_SCOPE")
	}
	_, _, section, parseErr := storage.ParseKnowledgeURI(uri)
	resourceText := item.Markdown
	if parseErr == nil && section != "" {
		sections, missing := selectSections(item.Payload.Sections, []string{section})
		if len(missing) > 0 {
			return nil, errors.New("SECTION_NOT_FOUND")
		}
		item.Payload.Sections = sections
		resourceText = "# " + item.Payload.Title + "\n\n## " + sections[0].Heading + " {#" + sections[0].ID + "}\n\n" + sections[0].Content + "\n"
	}
	if mode == "read" {
		item.Payload.Derivation = derivationSummary(item.Payload.Derivation)
	}
	resourceURI := item.URI + "?revision=" + fmt.Sprint(item.Revision)
	if section != "" {
		resourceURI = uri
	}
	return gin.H{"contents": []gin.H{{"uri": resourceURI, "mimeType": "text/markdown", "text": resourceText}}}, nil
}

func (s *Server) validateMCPKnowledgeReferences(ctx context.Context, c *gin.Context, payload model.KnowledgePayload) []string {
	issues := []string{}
	for _, source := range payload.Sources {
		if strings.HasPrefix(source.Resource, "kah://document/") {
			documentID, parseErr := storage.ParseDocumentURI(source.Resource)
			if parseErr != nil {
				issues = append(issues, "source "+source.ID+" has an invalid document URI")
				continue
			}
			detail, getErr := s.Store.GetDocument(ctx, documentID)
			if getErr != nil {
				issues = append(issues, "source "+source.ID+" document was not found")
				continue
			}
			if _, scopeErr := s.allowedMCPLibraries(c, []string{detail.LibraryID}); scopeErr != nil {
				issues = append(issues, "source "+source.ID+" is outside token scope")
				continue
			}
			if detail.Status != "ready" {
				issues = append(issues, "source "+source.ID+" document is not ready")
			}
			if source.Snapshot.ContentHash == "" || source.Snapshot.ContentHash != detail.ContentHash {
				issues = append(issues, "source "+source.ID+" document content hash does not match the current snapshot")
			}
			continue
		}
		if !strings.HasPrefix(source.Resource, "kah://knowledge/") {
			continue
		}
		item, err := s.Store.GetKnowledge(ctx, source.Resource, false)
		if err != nil {
			issues = append(issues, "source "+source.ID+" does not resolve to a stable KAH revision")
			continue
		}
		if _, err := s.allowedMCPLibraries(c, []string{item.LibraryID}); err != nil {
			issues = append(issues, "source "+source.ID+" is outside token scope")
		}
	}
	for _, relation := range payload.Relations {
		if !strings.HasPrefix(relation.Target, "kah://knowledge/") {
			continue
		}
		targetID, pinnedRevision, section, parseErr := storage.ParseKnowledgeURI(relation.Target)
		if parseErr != nil || section != "" || (pinnedRevision > 0 && relation.TargetRevision > 0 && pinnedRevision != relation.TargetRevision) {
			issues = append(issues, "relation "+relation.Type+" has an invalid target revision")
			continue
		}
		targetURI := storage.KnowledgeURI(targetID)
		if relation.TargetRevision > 0 {
			targetURI += "?revision=" + fmt.Sprint(relation.TargetRevision)
		} else if pinnedRevision > 0 {
			targetURI += "?revision=" + fmt.Sprint(pinnedRevision)
		}
		item, err := s.Store.GetKnowledge(ctx, targetURI, false)
		if err != nil {
			issues = append(issues, "relation "+relation.Type+" does not resolve to a stable KAH revision")
			continue
		}
		if _, err := s.allowedMCPLibraries(c, []string{item.LibraryID}); err != nil {
			issues = append(issues, "relation "+relation.Type+" is outside token scope")
		}
	}
	return issues
}

func documentMediaType(document model.Document) string {
	if strings.TrimSpace(document.MediaType) == "" {
		return "text/markdown"
	}
	return document.MediaType
}

func documentResourceText(detail model.DocumentDetail) string {
	parts := make([]string, 0, len(detail.Preview))
	for _, chunk := range detail.Preview {
		if text := strings.TrimSpace(chunk.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (s *Server) captureHTTPSnapshots(ctx context.Context, payload *model.KnowledgePayload) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(dialContext context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.LookupIP(host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range ips {
				if !isPublicHTTPSIP(ip) {
					lastErr = errors.New("HTTPS source resolves to a non-public address")
					continue
				}
				connection, dialErr := dialer.DialContext(dialContext, network, net.JoinHostPort(ip.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				lastErr = dialErr
			}
			if lastErr == nil {
				lastErr = errors.New("HTTPS source has no public address")
			}
			return nil, lastErr
		},
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport, CheckRedirect: func(request *http.Request, previous []*http.Request) error {
		if !isSafeHTTPSURL(request.URL) {
			return errors.New("HTTPS source redirect is not allowed")
		}
		return nil
	}}
	for index := range payload.Sources {
		source := &payload.Sources[index]
		if !strings.HasPrefix(source.Resource, "https://") {
			continue
		}
		source.Snapshot = model.KnowledgeSourceSnapshot{Status: "source-unverified"}
		parsed, parseErr := url.Parse(source.Resource)
		if parseErr != nil || !isSafeHTTPSURL(parsed) {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
			continue
		}
		hash := sha256.Sum256(body)
		source.Snapshot = model.KnowledgeSourceSnapshot{Status: "verified", ContentHash: hex.EncodeToString(hash[:]), CapturedAt: time.Now().UTC()}
	}
}

func isSafeHTTPSURL(value *url.URL) bool {
	if value == nil || value.Scheme != "https" || value.Hostname() == "" || value.User != nil {
		return false
	}
	if ip := net.ParseIP(value.Hostname()); ip != nil {
		return isPublicHTTPSIP(ip)
	}
	return true
}

func isPublicHTTPSIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

func selectSections(sections []model.KnowledgeSection, wanted []string) ([]model.KnowledgeSection, []string) {
	result := []model.KnowledgeSection{}
	missing := []string{}
	for _, section := range sections {
		if contains(wanted, section.ID) {
			result = append(result, section)
		}
	}
	for _, requested := range wanted {
		found := false
		for _, section := range sections {
			if section.ID == requested {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, requested)
		}
	}
	return result, missing
}

func mcpDraftVisible(c *gin.Context, item model.KnowledgeRevision) bool {
	if item.Status == "stable" || item.Status == "deprecated" {
		return true
	}
	value, _ := c.Get("identity")
	id, _ := value.(identity)
	return id.Desktop || contains(id.Token.Scopes, "mcp_manage")
}

func kahSubmissionDirectoryEntry(item model.KAHSubmission) model.KAHSubmissionDirectoryEntry {
	tags := item.Tags
	if tags == nil {
		tags = []string{}
	}
	return model.KAHSubmissionDirectoryEntry{
		ID: item.ID, LibraryID: item.LibraryID, KnowledgeURI: item.KnowledgeURI, Revision: item.Revision,
		Mode: item.Mode, ReviewStatus: item.ReviewStatus, Title: item.Title, Summary: item.Summary,
		Tags: tags, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func compareKAHKnowledge(submissionID string, candidate model.KnowledgeRevision, base *model.KnowledgeRevision) model.KAHKnowledgeComparison {
	comparison := model.KAHKnowledgeComparison{
		SubmissionID:    submissionID,
		Candidate:       comparisonRevision(candidate),
		HasBase:         base != nil,
		MetadataChanges: []model.KAHFieldChange{},
		SectionChanges:  []model.KAHSectionChange{},
		SourceChanges:   []model.KAHSourceChange{},
		RelationChanges: []model.KAHRelationChange{},
	}
	if base != nil {
		baseInfo := comparisonRevision(*base)
		comparison.Base = &baseInfo
	}
	candidateMetadata := comparableKnowledgeMetadata(candidate.Payload)
	baseMetadata := map[string]any{}
	if base != nil {
		baseMetadata = comparableKnowledgeMetadata(base.Payload)
	}
	metadataKeys := map[string]bool{}
	for key := range candidateMetadata {
		metadataKeys[key] = true
	}
	for key := range baseMetadata {
		metadataKeys[key] = true
	}
	sortedMetadataKeys := make([]string, 0, len(metadataKeys))
	for key := range metadataKeys {
		sortedMetadataKeys = append(sortedMetadataKeys, key)
	}
	sort.Strings(sortedMetadataKeys)
	for _, key := range sortedMetadataKeys {
		before, beforeOK := baseMetadata[key]
		after, afterOK := candidateMetadata[key]
		if !beforeOK {
			before = nil
		}
		if !afterOK {
			after = nil
		}
		if !reflect.DeepEqual(before, after) {
			comparison.MetadataChanges = append(comparison.MetadataChanges, model.KAHFieldChange{Field: key, Before: before, After: after})
		}
	}

	candidateSections := map[string]model.KnowledgeSection{}
	for _, section := range candidate.Payload.Sections {
		candidateSections[section.ID] = section
	}
	baseSections := map[string]model.KnowledgeSection{}
	if base != nil {
		for _, section := range base.Payload.Sections {
			baseSections[section.ID] = section
		}
	}
	sectionIDs := sortedUnionKeys(candidateSections, baseSections)
	for _, id := range sectionIDs {
		before, beforeOK := baseSections[id]
		after, afterOK := candidateSections[id]
		switch {
		case !beforeOK:
			value := after
			comparison.SectionChanges = append(comparison.SectionChanges, model.KAHSectionChange{ID: id, Change: "added", After: &value})
			comparison.Summary.SectionAdds++
		case !afterOK:
			value := before
			comparison.SectionChanges = append(comparison.SectionChanges, model.KAHSectionChange{ID: id, Change: "removed", Before: &value})
			comparison.Summary.SectionRemoves++
		case !reflect.DeepEqual(before, after):
			beforeValue, afterValue := before, after
			comparison.SectionChanges = append(comparison.SectionChanges, model.KAHSectionChange{ID: id, Change: "modified", Before: &beforeValue, After: &afterValue})
			comparison.Summary.SectionChanges++
		}
	}

	candidateSources := map[string]model.KnowledgeSource{}
	for _, source := range candidate.Payload.Sources {
		candidateSources[source.ID] = source
	}
	baseSources := map[string]model.KnowledgeSource{}
	if base != nil {
		for _, source := range base.Payload.Sources {
			baseSources[source.ID] = source
		}
	}
	sourceIDs := sortedUnionKeys(candidateSources, baseSources)
	for _, id := range sourceIDs {
		before, beforeOK := baseSources[id]
		after, afterOK := candidateSources[id]
		switch {
		case !beforeOK:
			value := after
			comparison.SourceChanges = append(comparison.SourceChanges, model.KAHSourceChange{ID: id, Change: "added", After: &value})
			comparison.Summary.SourceAdds++
		case !afterOK:
			value := before
			comparison.SourceChanges = append(comparison.SourceChanges, model.KAHSourceChange{ID: id, Change: "removed", Before: &value})
			comparison.Summary.SourceRemoves++
		case !reflect.DeepEqual(before, after):
			beforeValue, afterValue := before, after
			comparison.SourceChanges = append(comparison.SourceChanges, model.KAHSourceChange{ID: id, Change: "modified", Before: &beforeValue, After: &afterValue})
			comparison.Summary.SourceChanges++
		}
	}

	candidateRelations := map[string]model.KnowledgeRelation{}
	for _, relation := range candidate.Payload.Relations {
		candidateRelations[relationComparisonKey(relation)] = relation
	}
	baseRelations := map[string]model.KnowledgeRelation{}
	if base != nil {
		for _, relation := range base.Payload.Relations {
			baseRelations[relationComparisonKey(relation)] = relation
		}
	}
	relationKeys := sortedUnionKeys(candidateRelations, baseRelations)
	for _, key := range relationKeys {
		before, beforeOK := baseRelations[key]
		after, afterOK := candidateRelations[key]
		switch {
		case !beforeOK:
			value := after
			comparison.RelationChanges = append(comparison.RelationChanges, model.KAHRelationChange{Key: key, Change: "added", After: &value})
			comparison.Summary.RelationAdds++
		case !afterOK:
			value := before
			comparison.RelationChanges = append(comparison.RelationChanges, model.KAHRelationChange{Key: key, Change: "removed", Before: &value})
			comparison.Summary.RelationRemoves++
		}
	}
	comparison.Summary.MetadataChanges = len(comparison.MetadataChanges)
	comparison.Changed = comparison.Summary.MetadataChanges > 0 || len(comparison.SectionChanges) > 0 || len(comparison.SourceChanges) > 0 || len(comparison.RelationChanges) > 0
	return comparison
}

func comparisonRevision(value model.KnowledgeRevision) model.KAHComparisonRevision {
	return model.KAHComparisonRevision{URI: value.URI, Revision: value.Revision, Status: value.Status, Title: value.Payload.Title, Description: value.Payload.Description}
}

func comparableKnowledgeMetadata(payload model.KnowledgePayload) map[string]any {
	aliases := append([]string{}, payload.Aliases...)
	primaryPath := append([]string{}, payload.PrimaryPath...)
	tags := append([]string{}, payload.Tags...)
	sort.Strings(aliases)
	sort.Strings(primaryPath)
	sort.Strings(tags)
	classifications := map[string][]string{}
	for key, values := range payload.Classifications {
		copyValues := append([]string{}, values...)
		sort.Strings(copyValues)
		classifications[key] = copyValues
	}
	return map[string]any{
		"type": payload.Type, "subtype": payload.Subtype, "title": payload.Title, "description": payload.Description,
		"language": payload.Language, "aliases": aliases, "primary_path": primaryPath,
		"classifications": classifications, "tags": tags, "duplicate_intent": payload.DuplicateIntent,
	}
}

func sortedUnionKeys[T any](left, right map[string]T) []string {
	keys := map[string]bool{}
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func relationComparisonKey(relation model.KnowledgeRelation) string {
	return fmt.Sprintf("%s|%s|%d", relation.Type, relation.Target, relation.TargetRevision)
}

func comparisonMessage(value model.KAHKnowledgeComparison) string {
	if !value.HasBase {
		return fmt.Sprintf("Submission %s compared without a baseline; %d sections and %d sources are present.", value.SubmissionID, value.Summary.SectionAdds, value.Summary.SourceAdds)
	}
	return fmt.Sprintf("Submission %s compared with %s; changed=%t, metadata=%d, sections +%d/-%d/~%d, sources +%d/-%d/~%d, relations +%d/-%d.", value.SubmissionID, value.Base.URI, value.Changed, value.Summary.MetadataChanges, value.Summary.SectionAdds, value.Summary.SectionRemoves, value.Summary.SectionChanges, value.Summary.SourceAdds, value.Summary.SourceRemoves, value.Summary.SourceChanges, value.Summary.RelationAdds, value.Summary.RelationRemoves)
}
func derivationSummary(value *model.KnowledgeDerivation) *model.KnowledgeDerivation {
	if value == nil {
		return nil
	}
	return &model.KnowledgeDerivation{ID: value.ID, Premises: value.Premises, Method: value.Method, Conclusion: value.Conclusion, Limitations: value.Limitations, Uncertainty: value.Uncertainty}
}
func mustJSONString(value any) string { bytes, _ := json.Marshal(value); return string(bytes) }
func validationMessage(value model.KnowledgeValidation) string {
	if value.Valid {
		return "Candidate is valid."
	}
	messages := []string{}
	for _, issue := range value.Errors {
		messages = append(messages, issue.Code+": "+issue.Message)
	}
	return strings.Join(messages, "\n")
}

const kahSchemaReference = `{"schema":"kah-knowledge/v1","types":["concept","claim","procedure","decision","policy","reference"],"languages":["zh-CN","en"],"relations":["broader","part_of","related","depends_on","applies_to","example_of","supports","contradicts","derived_from","supersedes","translation_of"]}`

const kahReadSkill = `---
name: kah-knowledge-read
description: Search and cite KAH knowledge through the Read MCP server. Use when an Agent needs evidence-backed knowledge from a KAH library; do not use to submit or edit knowledge.
---

# KAH Knowledge Read

1. Call knowledge_search with the task and narrow filters when available.
2. Treat the returned directory as an index. Select relevant URIs before reading content.
3. Call knowledge_get with only needed section IDs; use resources/read for the canonical exported Markdown.
4. Cite the knowledge URI, exact revision, section ID, and cited source locator. Surface stale or disputed warnings.

Read kah://schema/kah-knowledge/v1 before forming an unsupported assumption about the format.`

const kahManageSkill = `---
name: kah-knowledge-manage
description: Search, compare, validate, submit, and review KAH Knowledge Profile v1 through the Manage MCP server. Agent approval requires evidence-backed confidence above 95 percent.
---

# KAH Knowledge Manage

1. Search first for duplicates and existing revisions.
2. Call knowledge_submission_list with statuses=["pending_review"] when managing the review queue, then call knowledge_submission_get for a selected submission.
3. Gather exact sources, source locators, and body citations. Do not invent citations.
4. Call knowledge_compare before reviewing. It compares metadata, sections, sources, and relations against a selected baseline.
5. Build a KAH v1 JSON candidate and call knowledge_validate.
6. Resolve every blocking error. For near duplicates choose revision, supplement, or independent through duplicate_intent and relations.
7. Call knowledge_submit with a fresh idempotencyKey. The result is a review-gated draft, never an approval.
8. Call knowledge_review with decision, confidence in the 0..1 range, and a reason. Only confidence strictly greater than 0.95 can approve and publish; 0.95 or lower is recorded as needs_human and remains pending_review.
9. If more evidence is needed, submit a new immutable revision with supplementary sources before asking for another Agent review. Never retry the same deferred submission with an inflated confidence.
10. Treat approval as complete only when the response has published=true and submission.reviewStatus=published. Rejection requires a concrete reason.

Read kah://schema/kah-knowledge/v1 for the complete constrained vocabulary. Do not call desktop-only HTTP review routes, edit the KAH database, or mutate source documents.`

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
)

func seedStableKnowledge(t *testing.T, server *Server, libraryID string) model.KnowledgeRevision {
	t.Helper()
	payload := model.KnowledgePayload{
		Schema: "kah-knowledge/v1", Type: "claim", Title: "KAH MCP 搜索规则", Description: "搜索先返回目录，再读取具体章节。", Language: "zh-CN",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "主张", Content: "Agent 必须先搜索知识目录。"}},
	}
	submission, _, err := server.Store.CreateKnowledgeDraft(context.Background(), storage.KnowledgeDraftInput{LibraryID: libraryID, ClientSubmissionID: "seed-stable", Mode: "create", Payload: payload, RequireSources: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Store.ReviewKAHSubmission(context.Background(), submission.ID, "desktop", "approve", "seed"); err != nil {
		t.Fatal(err)
	}
	item, err := server.Store.PublishKAHSubmission(context.Background(), submission.ID)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func callMCP(t *testing.T, handler http.Handler, path, token, method string, params any) map[string]any {
	t.Helper()
	response := request(t, handler, http.MethodPost, path, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, token)
	if response.Code != http.StatusOK {
		t.Fatalf("MCP %s: %d %s", method, response.Code, response.Body.String())
	}
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestMCPReadSearchDirectoryAndResource(t *testing.T) {
	server, handler := testServer(t)
	created := request(t, handler, http.MethodPost, "/api/v1/libraries", map[string]any{"name": "MCP"}, "desktop-test")
	var library model.Library
	if err := json.Unmarshal(created.Body.Bytes(), &library); err != nil {
		t.Fatal(err)
	}
	document := createReadyImportDocument(t, server, library.ID, "mcp-source.md", "mcp-source-hash")
	stable := seedStableKnowledge(t, server, library.ID)
	tokenResponse := request(t, handler, http.MethodPost, "/api/v1/tokens", map[string]any{"name": "read", "scopes": []string{"mcp_read"}, "libraryIds": []string{library.ID}}, "desktop-test")
	if tokenResponse.Code != http.StatusCreated {
		t.Fatalf("create token: %d %s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var token model.AgentToken
	if err := json.Unmarshal(tokenResponse.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}

	initialized := callMCP(t, handler, "/mcp/read", token.Secret, "initialize", map[string]any{"protocolVersion": mcpProtocolVersion})
	if initialized["result"] == nil {
		t.Fatalf("initialize failed: %#v", initialized)
	}
	templates := callMCP(t, handler, "/mcp/read", token.Secret, "resources/templates/list", map[string]any{})
	templateResult, ok := templates["result"].(map[string]any)
	templateList, listOK := templateResult["resourceTemplates"].([]any)
	if !ok || !listOK || len(templateList) != 2 {
		t.Fatalf("resource template contract missing: %#v", templates)
	}
	resources := callMCP(t, handler, "/mcp/read", token.Secret, "resources/list", map[string]any{})
	resourceResult, resourceOK := resources["result"].(map[string]any)
	resourceList, resourcesOK := resourceResult["resources"].([]any)
	if !resourceOK || !resourcesOK {
		t.Fatalf("resource listing missing: %#v", resources)
	}
	foundDocument := false
	for _, rawResource := range resourceList {
		item, itemOK := rawResource.(map[string]any)
		if itemOK && item["uri"] == storage.DocumentURI(document.ID) {
			foundDocument = true
			break
		}
	}
	if !foundDocument {
		t.Fatalf("imported document was not listed as an MCP resource: %#v", resources)
	}
	search := callMCP(t, handler, "/mcp/read", token.Secret, "tools/call", map[string]any{"name": "knowledge_search", "arguments": map[string]any{"query": "目录", "libraryIds": []string{library.ID}}})
	result := search["result"].(map[string]any)
	if result["structuredContent"] == nil {
		t.Fatalf("search did not return a directory: %#v", search)
	}
	get := callMCP(t, handler, "/mcp/read", token.Secret, "tools/call", map[string]any{"name": "knowledge_get", "arguments": map[string]any{"uri": stable.URI, "sectionIds": []string{"claim"}}})
	if get["result"] == nil {
		t.Fatalf("get failed: %#v", get)
	}
	resource := callMCP(t, handler, "/mcp/read", token.Secret, "resources/read", map[string]any{"uri": stable.URI + "?revision=1#claim"})
	if resource["result"] == nil {
		t.Fatalf("resource read failed: %#v", resource)
	}
	documentResource := callMCP(t, handler, "/mcp/read", token.Secret, "resources/read", map[string]any{"uri": storage.DocumentURI(document.ID)})
	documentResult, documentResultOK := documentResource["result"].(map[string]any)
	contents, contentsOK := documentResult["contents"].([]any)
	if !documentResultOK || !contentsOK || len(contents) != 1 {
		t.Fatalf("document resource read failed: %#v", documentResource)
	}
	content, contentOK := contents[0].(map[string]any)
	if !contentOK || !strings.Contains(content["text"].(string), "可引用的来源正文") {
		t.Fatalf("document resource did not preserve indexed text: %#v", documentResource)
	}
}

func TestMCPDocumentSourceValidationPinsCurrentSnapshot(t *testing.T) {
	server, handler := testServer(t)
	created := request(t, handler, http.MethodPost, "/api/v1/libraries", map[string]any{"name": "MCP documents"}, "desktop-test")
	var library model.Library
	if err := json.Unmarshal(created.Body.Bytes(), &library); err != nil {
		t.Fatal(err)
	}
	document := createReadyImportDocument(t, server, library.ID, "source.md", "source-hash")
	tokenResponse := request(t, handler, http.MethodPost, "/api/v1/tokens", map[string]any{"name": "manage", "scopes": []string{"mcp_manage"}, "libraryIds": []string{library.ID}}, "desktop-test")
	var token model.AgentToken
	if err := json.Unmarshal(tokenResponse.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	candidate := map[string]any{
		"schema": "kah-knowledge/v1", "type": "claim", "title": "导入文档来源", "description": "验证 MCP 草稿绑定当前文档快照。", "language": "zh-CN", "duplicate_intent": "independent",
		"sections": []map[string]any{{"id": "claim", "heading": "主张", "content": "导入文档可以作为有快照的知识来源。[^doc]"}},
		"sources":  []map[string]any{{"id": "doc", "resource": storage.DocumentURI(document.ID), "title": document.Title, "snapshot": map[string]any{"status": "captured", "content_hash": document.ContentHash}}},
	}
	validated := callMCP(t, handler, "/mcp/manage", token.Secret, "tools/call", map[string]any{"name": "knowledge_validate", "arguments": map[string]any{"libraryId": library.ID, "candidate": candidate}})
	validationResult, ok := validated["result"].(map[string]any)
	if !ok || validationResult["isError"] == true {
		t.Fatalf("valid document source was rejected: %#v", validated)
	}
	wrongHash := map[string]any{
		"schema": "kah-knowledge/v1", "type": "claim", "title": "错误快照", "description": "验证过期文档来源会被拒绝。", "language": "zh-CN",
		"sections": []map[string]any{{"id": "claim", "heading": "主张", "content": "来源快照必须一致。[^doc]"}},
		"sources":  []map[string]any{{"id": "doc", "resource": storage.DocumentURI(document.ID), "title": document.Title, "snapshot": map[string]any{"status": "captured", "content_hash": "stale-hash"}}},
	}
	invalid := callMCP(t, handler, "/mcp/manage", token.Secret, "tools/call", map[string]any{"name": "knowledge_validate", "arguments": map[string]any{"libraryId": library.ID, "candidate": wrongHash}})
	invalidResult, invalidOK := invalid["result"].(map[string]any)
	if !invalidOK || invalidResult["isError"] != true {
		t.Fatalf("stale document snapshot was not rejected: %#v", invalid)
	}
	submitted := callMCP(t, handler, "/mcp/manage", token.Secret, "tools/call", map[string]any{"name": "knowledge_submit", "arguments": map[string]any{"libraryId": library.ID, "mode": "create", "candidate": candidate, "idempotencyKey": "document-source-1"}})
	submittedResult, submittedOK := submitted["result"].(map[string]any)
	if !submittedOK {
		t.Fatalf("document source submission failed: %#v", submitted)
	}
	structured, structuredOK := submittedResult["structuredContent"].(map[string]any)
	if !structuredOK || structured["reviewStatus"] != "pending_review" {
		t.Fatalf("document source submission was not review-gated: %#v", submitted)
	}
}

func TestMCPManageSubmitsDraftAndCannotPublish(t *testing.T) {
	server, handler := testServer(t)
	created := request(t, handler, http.MethodPost, "/api/v1/libraries", map[string]any{"name": "Manage"}, "desktop-test")
	var library model.Library
	if err := json.Unmarshal(created.Body.Bytes(), &library); err != nil {
		t.Fatal(err)
	}
	stable := seedStableKnowledge(t, server, library.ID)
	tokenResponse := request(t, handler, http.MethodPost, "/api/v1/tokens", map[string]any{"name": "manage", "scopes": []string{"mcp_manage"}, "libraryIds": []string{library.ID}}, "desktop-test")
	var token model.AgentToken
	if err := json.Unmarshal(tokenResponse.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	candidate := map[string]any{
		"schema": "kah-knowledge/v1", "type": "claim", "title": "引用已有 KAH 知识", "description": "Manage MCP 需要来源和正文引用。", "language": "zh-CN", "duplicate_intent": "independent",
		"sections": []map[string]any{{"id": "claim", "heading": "主张", "content": "提交前必须验证来源。[^seed]"}},
		"sources":  []map[string]any{{"id": "seed", "resource": stable.URI + "?revision=1", "title": stable.Payload.Title}},
	}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	validated := callMCP(t, handler, "/mcp/manage", token.Secret, "tools/call", map[string]any{"name": "knowledge_validate", "arguments": map[string]any{"libraryId": library.ID, "candidate": string(candidateJSON)}})
	if validated["result"] == nil {
		t.Fatalf("validation failed: %#v", validated)
	}
	submitted := callMCP(t, handler, "/mcp/manage", token.Secret, "tools/call", map[string]any{"name": "knowledge_submit", "arguments": map[string]any{"libraryId": library.ID, "mode": "create", "candidate": string(candidateJSON), "idempotencyKey": "managed-1"}})
	result := submitted["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if structured["reviewStatus"] != "pending_review" {
		t.Fatalf("Manage MCP published unexpectedly: %#v", structured)
	}
	denied := request(t, handler, http.MethodPost, "/mcp/read", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "knowledge_submit", "arguments": map[string]any{}}}, token.Secret)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("manage token should not access read endpoint: %d %s", denied.Code, denied.Body.String())
	}
}

func TestLegacyAgentHTTPRoutesAreRemoved(t *testing.T) {
	_, handler := testServer(t)
	for _, path := range []string{"/api/v1/query", "/api/v1/query/stream", "/api/v1/skills/query", "/api/v1/knowledge-submissions"} {
		response := request(t, handler, http.MethodPost, path, map[string]any{}, "desktop-test")
		if response.Code != http.StatusNotFound {
			t.Fatalf("legacy route %s was not removed: %d %s", path, response.Code, response.Body.String())
		}
	}
}

func TestDesktopKAHReviewPublishesAndPreservesHistoricalRevision(t *testing.T) {
	server, handler := testServer(t)
	created := request(t, handler, http.MethodPost, "/api/v1/libraries", map[string]any{"name": "Desktop KAH"}, "desktop-test")
	var library model.Library
	if err := json.Unmarshal(created.Body.Bytes(), &library); err != nil {
		t.Fatal(err)
	}
	submission, _, err := server.Store.CreateKnowledgeDraft(context.Background(), storage.KnowledgeDraftInput{
		LibraryID: library.ID, ClientSubmissionID: "desktop-review-1", Mode: "create", Payload: model.KnowledgePayload{
			Schema: "kah-knowledge/v1", Type: "claim", Title: "桌面审核主张", Description: "只有批准后才能进入目录。", Language: "zh-CN",
			Sections: []model.KnowledgeSection{{ID: "claim", Heading: "主张", Content: "桌面审核完成后才可检索。"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	approved := request(t, handler, http.MethodPost, "/api/v1/knowledge/submissions/"+submission.ID+"/approve", map[string]any{"reason": "人工核验通过"}, "desktop-test")
	if approved.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", approved.Code, approved.Body.String())
	}
	var published model.KnowledgeRevision
	if err := json.Unmarshal(approved.Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	if published.Status != "stable" || !published.Stable {
		t.Fatalf("published KAH = %+v", published)
	}
	resolved := request(t, handler, http.MethodGet, "/api/v1/knowledge/resolve?uri="+published.URI, nil, "desktop-test")
	if resolved.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", resolved.Code, resolved.Body.String())
	}
	listed := request(t, handler, http.MethodGet, "/api/v1/knowledge/submissions?libraryId="+library.ID, nil, "desktop-test")
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte(submission.ID)) {
		t.Fatalf("list submissions: %d %s", listed.Code, listed.Body.String())
	}
}

func TestMCPRejectsUnsafeOriginAndUnknownSection(t *testing.T) {
	server, handler := testServer(t)
	created := request(t, handler, http.MethodPost, "/api/v1/libraries", map[string]any{"name": "MCP origin"}, "desktop-test")
	var library model.Library
	if err := json.Unmarshal(created.Body.Bytes(), &library); err != nil {
		t.Fatal(err)
	}
	stable := seedStableKnowledge(t, server, library.ID)
	tokenResponse := request(t, handler, http.MethodPost, "/api/v1/tokens", map[string]any{"name": "read", "scopes": []string{"mcp_read"}, "libraryIds": []string{library.ID}}, "desktop-test")
	var token model.AgentToken
	if err := json.Unmarshal(tokenResponse.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "knowledge_get", "arguments": map[string]any{"uri": stable.URI, "sectionIds": []string{"missing"}}}}); err != nil {
		t.Fatal(err)
	}
	unsafe := httptest.NewRequest(http.MethodPost, "/mcp/read", &payload)
	unsafe.Header.Set("Content-Type", "application/json")
	unsafe.Header.Set("Origin", "https://evil.example")
	unsafe.Header.Set("Authorization", "Bearer "+token.Secret)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, unsafe)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unsafe origin status = %d, want 403", response.Code)
	}
	known := callMCP(t, handler, "/mcp/read", token.Secret, "tools/call", map[string]any{"name": "knowledge_get", "arguments": map[string]any{"uri": stable.URI, "sectionIds": []string{"missing"}}})
	result, ok := known["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("unknown section was not rejected: %#v", known)
	}
}

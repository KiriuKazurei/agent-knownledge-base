package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
)

func TestImportedReferenceSectionsCoverLongSourceInOrder(t *testing.T) {
	chunks := []model.Chunk{
		{ID: "first", Text: "第一段资料。" + strings.Repeat("甲", importedReferenceSectionRunes)},
		{ID: "second", Text: "第二段资料。"},
	}
	sections := importedReferenceSections(chunks, "zh-CN")
	if len(sections) < 2 {
		t.Fatalf("expected long source to be split into sections, got %d", len(sections))
	}
	joined := ""
	for _, section := range sections {
		if !strings.Contains(section.Content, "[^source]") {
			t.Fatalf("section lost source citation: %#v", section)
		}
		joined += section.Content
	}
	if !strings.Contains(joined, "第一段资料") || !strings.Contains(joined, "第二段资料") {
		t.Fatalf("source sections lost content: %q", joined[:min(len(joined), 120)])
	}
}

func TestImportedReferenceSectionsRenderRetainedOCRTable(t *testing.T) {
	chunks := []model.Chunk{{
		Text: "表格原始文本仍需保留。",
		Location: map[string]any{
			"page": float64(2),
			"ocrLayout": map[string]any{"tables": []any{map[string]any{
				"headers": []any{"编号", "状态"},
				"rows":    []any{[]any{"T01", "通过"}, []any{"T02", "复核"}},
			}}},
		},
	}}
	sections := importedReferenceSections(chunks, "zh-CN")
	if len(sections) != 1 {
		t.Fatalf("sections = %#v", sections)
	}
	content := sections[0].Content
	for _, expected := range []string{
		"表格原始文本仍需保留。", "#### 第2页", "按 OCR 坐标恢复的表格", "| 编号 | 状态 |", "| T01 | 通过 |", "| T02 | 复核 |", "[^source]",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("structured OCR reference lost %q: %s", expected, content)
		}
	}
}

func TestImportedSummaryBatchesCoverAllSourceText(t *testing.T) {
	text := strings.Repeat("甲", importedSummaryMaxRunes+17)
	batches := importedSummaryBatches([]model.Chunk{{ID: "source", Text: text}}, importedSummaryMaxRunes)
	if len(batches) != 2 {
		t.Fatalf("expected two batches, got %d", len(batches))
	}
	joined := ""
	for _, batch := range batches {
		joined += importedReferenceExtract(batch, importedSummaryMaxRunes)
	}
	if joined != text {
		t.Fatalf("batched source changed: got %d runes, want %d", len([]rune(joined)), len([]rune(text)))
	}
}

func TestRestoreImportedSummaryCheckpointSkipsCompletedBatches(t *testing.T) {
	expected := importedSummaryCheckpoint{Version: 1, DocumentID: "document-1", ContentHash: "hash-1", ProviderID: "provider-1", PromptRevision: importedSummaryPromptRevision}
	expected.Candidates = []model.KnowledgePayload{{Title: "已完成批次", Description: "候选", Language: "zh-CN", Sections: []model.KnowledgeSection{{ID: "definition", Content: "证据[^source]"}}}}
	expected.NextBatch = 1
	data, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	restored, next := restoreImportedSummaryCheckpoint(data, importedSummaryCheckpoint{Version: 1, DocumentID: "document-1", ContentHash: "hash-1", ProviderID: "provider-1", PromptRevision: importedSummaryPromptRevision}, 3)
	if next != 1 || len(restored.Candidates) != 1 || restored.Candidates[0].Title != "已完成批次" {
		t.Fatalf("checkpoint was not restored: next=%d value=%+v", next, restored)
	}
	_, next = restoreImportedSummaryCheckpoint(data, importedSummaryCheckpoint{Version: 1, DocumentID: "document-1", ContentHash: "changed", ProviderID: "provider-1", PromptRevision: importedSummaryPromptRevision}, 3)
	if next != 0 {
		t.Fatal("checkpoint from a different source hash must be ignored")
	}
}

func TestImportConversionModeOffSkipsKAHDraftCreation(t *testing.T) {
	server, _ := testServer(t)
	ctx := context.Background()
	library, err := server.Store.CreateLibrary(ctx, "关闭转换", "")
	if err != nil {
		t.Fatal(err)
	}
	off := "off"
	if _, err := server.Store.UpdateLibraryPatch(ctx, library.ID, storage.LibraryPatch{ImportConversionMode: &off}); err != nil {
		t.Fatal(err)
	}
	document, err := server.Store.CreatePendingDocument(ctx, library.ID, "raw.md", "text/markdown", "raw.md", "", "", "raw-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Store.ReplaceChunks(ctx, document.ID, []model.Chunk{{ID: "raw-1", DocumentID: document.ID, Text: "原始资料", Location: map[string]any{"ordinal": 0}, ContentHash: "chunk-hash"}}); err != nil {
		t.Fatal(err)
	}
	_, summaryQueued, referenceCreated, err := server.queueImportedKnowledgeProcessing(ctx, document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summaryQueued || referenceCreated {
		t.Fatalf("off mode queued KAH work: summary=%v reference=%v", summaryQueued, referenceCreated)
	}
	items, err := server.Store.ListKAHSubmissions(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("off mode created unexpected KAH submissions: %+v", items)
	}
}

func TestMergeImportedSummaryCandidatesPinsCompleteSource(t *testing.T) {
	detail := model.DocumentDetail{Document: model.Document{ID: "document-1", Title: "long.md", ContentHash: "hash-1"}, Preview: []model.Chunk{{ID: "one", Text: "第一批"}, {ID: "two", Text: "第二批"}}}
	first := model.KnowledgePayload{
		Schema: "kah-knowledge/v1", Type: "concept", Title: "批量概念", Description: "描述", Language: "zh-CN",
		Sections: []model.KnowledgeSection{{ID: "definition", Heading: "定义", Content: "第一批[^source]"}},
		Tags:     []string{"first"},
	}
	second := model.KnowledgePayload{
		Schema: "kah-knowledge/v1", Type: "concept", Title: "批量概念", Description: "描述", Language: "zh-CN",
		Sections: []model.KnowledgeSection{{ID: "definition", Heading: "定义", Content: "第二批[^source]"}},
		Tags:     []string{"second"},
	}
	merged, err := mergeImportedSummaryCandidates([]model.KnowledgePayload{first, second}, detail)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Derivation == nil || merged.Derivation.Method != "model-synthesis-batched" {
		t.Fatalf("missing batched derivation: %#v", merged.Derivation)
	}
	if len(merged.Sections) != 2 || merged.Sections[1].ID != "batch-002-definition" {
		t.Fatalf("sections were not namespaced: %#v", merged.Sections)
	}
	if len(merged.Sources) != 1 || merged.Sources[0].Locator["chunkCount"] != 2 {
		t.Fatalf("source locator did not cover full source: %#v", merged.Sources)
	}
}

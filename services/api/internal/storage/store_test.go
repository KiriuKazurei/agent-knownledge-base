package storage

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func TestPortableObjectAndSearch(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, err := store.CreateLibrary(ctx, "Test Library", "")
	if err != nil {
		t.Fatal(err)
	}
	relative, digest, err := store.PutObject(strings.NewReader("portable knowledge text"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(relative) {
		t.Fatalf("stored path is absolute: %s", relative)
	}
	doc, err := store.CreatePendingDocument(ctx, library.ID, "note.md", "text/markdown", "note.md", "", relative, digest)
	if err != nil {
		t.Fatal(err)
	}
	chunk := model.Chunk{ID: "chunk-1", DocumentID: doc.ID, Text: "portable knowledge text", Location: map[string]any{"line": 1}, ContentHash: digest}
	if err := store.ReplaceChunks(ctx, doc.ID, []model.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	request := model.QueryRequest{Query: "knowledge", TopK: 10}
	results, err := store.Search(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	resolved, err := store.Resolve(relative)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(resolved); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRejectsTraversal(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Resolve("../outside"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestSkillImportLinksAndSearch(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, err := store.CreateLibrary(ctx, "PDF Library", "")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "Skill.md")
	content := "---\nname: pdf-processing\ndescription: Extract and transform PDF files when working with PDF documents.\n---\n\n# PDF Processing\n\nUse the PDF workflow.\n"
	if err := os.WriteFile(source, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	item, err := store.ImportSkill(ctx, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "pdf-processing" || item.RootPath != "skills/pdf-processing" || item.FileCount != 1 {
		t.Fatalf("unexpected Skill: %+v", item)
	}
	files, err := store.SkillFiles(ctx, item.ID)
	if err != nil || len(files) != 1 || files[0].Path != "SKILL.md" {
		t.Fatalf("unexpected Skill files: %v %+v", err, files)
	}
	target, _, err := store.ReadSkillFile(ctx, item.ID, "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
	item, err = store.SetSkillLinks(ctx, item.ID, []string{library.ID}, []string{library.ID})
	if err != nil || len(item.UsesLibraryIDs) != 1 || len(item.RequiresLibraryIDs) != 1 {
		t.Fatalf("unexpected links: %v %+v", err, item)
	}
	results, err := store.SearchSkills(ctx, model.SkillQueryRequest{Query: "PDF workflow", LibraryIDs: []string{library.ID}, TopK: 10})
	if err != nil || len(results) != 1 || results[0].Name != "pdf-processing" {
		t.Fatalf("unexpected search: %v %+v", err, results)
	}
	required, err := store.RequiredSkills(ctx, []string{library.ID})
	if err != nil || len(required) != 1 || required[0].ID != item.ID {
		t.Fatalf("unexpected required Skills: %v %+v", err, required)
	}
	if _, err := store.ImportSkill(ctx, source, false); !errors.Is(err, ErrSkillConflict) {
		t.Fatalf("expected Skill conflict, got %v", err)
	}
}

func TestSkillZipRejectsTraversal(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	archivePath := filepath.Join(t.TempDir(), "bad.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("../SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("---\nname: bad\ndescription: Bad package\n---\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportSkill(context.Background(), archivePath, false); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestSkillZipImportsPackageWithPlatformMetadata(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	archivePath := filepath.Join(t.TempDir(), "portable-skill.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entries := map[string]string{
		"portable-skill/SKILL.md":            "---\nname: portable-skill\ndescription: Import a standard Skill package created on macOS.\n---\n\n# Portable Skill\n",
		"portable-skill/references/guide.md": "# Guide\n",
		"__MACOSX/portable-skill/._SKILL.md": "metadata",
	}
	for path, content := range entries {
		entry, err := writer.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	item, err := store.ImportSkill(context.Background(), archivePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "portable-skill" || item.FileCount != 2 {
		t.Fatalf("unexpected imported Skill: %+v", item)
	}
	files, err := store.SkillFiles(context.Background(), item.ID)
	if err != nil || len(files) != 2 || files[1].Path != "references/guide.md" {
		t.Fatalf("unexpected Skill files: %v %+v", err, files)
	}
}

func TestKAHRevisionIsImmutableAndStablePointerMovesAtomically(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, err := store.CreateLibrary(ctx, "KAH revisions", "")
	if err != nil {
		t.Fatal(err)
	}
	firstPayload := model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "原始主张", Description: "用于验证稳定指针。", Language: "zh-CN",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "主张", Content: "这是不可变的第一版。"}},
	}
	first, _, err := store.CreateKnowledgeDraft(ctx, KnowledgeDraftInput{LibraryID: library.ID, ClientSubmissionID: "revision-1", Mode: "create", Payload: firstPayload})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReviewKAHSubmission(ctx, first.ID, "tester", "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishKAHSubmission(ctx, first.ID); err != nil {
		t.Fatal(err)
	}

	secondPayload := firstPayload
	secondPayload.Title = "修订后的主张"
	secondPayload.Sections = []model.KnowledgeSection{{ID: "claim", Heading: "主张", Content: "这是不可变的第二版。"}}
	second, _, err := store.CreateKnowledgeDraft(ctx, KnowledgeDraftInput{LibraryID: library.ID, ClientSubmissionID: "revision-2", Mode: "propose_revision", BaseURI: first.KnowledgeURI, Payload: secondPayload})
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 {
		t.Fatalf("revision = %d, want 2", second.Revision)
	}
	if _, err = store.ReviewKAHSubmission(ctx, second.ID, "tester", "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishKAHSubmission(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	stable, err := store.GetKnowledge(ctx, first.KnowledgeURI, false)
	if err != nil {
		t.Fatal(err)
	}
	if stable.Revision != 2 || stable.Payload.Title != "修订后的主张" {
		t.Fatalf("stable revision = %+v, want revision 2", stable)
	}
	original, err := store.GetKnowledge(ctx, first.KnowledgeURI+"?revision=1", false)
	if err != nil {
		t.Fatal(err)
	}
	if original.Revision != 1 || original.Payload.Title != "原始主张" || original.Payload.Sections[0].Content != "这是不可变的第一版。" {
		t.Fatalf("original revision was mutated: %+v", original.Payload)
	}
}

func TestKAHUnverifiedHTTPSSourceCannotPublish(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, err := store.CreateLibrary(ctx, "KAH sources", "")
	if err != nil {
		t.Fatal(err)
	}
	payload := model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "未验证网页来源", Description: "发布必须等待来源快照验证。", Language: "zh-CN",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "主张", Content: "这个主张引用网页。[^web]"}},
		Sources:  []model.KnowledgeSource{{ID: "web", Resource: "https://example.invalid/evidence"}},
	}
	submission, _, err := store.CreateKnowledgeDraft(ctx, KnowledgeDraftInput{LibraryID: library.ID, ClientSubmissionID: "unverified-web", Mode: "create", Payload: payload, RequireSources: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReviewKAHSubmission(ctx, submission.ID, "tester", "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishKAHSubmission(ctx, submission.ID); err == nil || err.Error() != "SOURCE_UNVERIFIED" {
		t.Fatalf("publish error = %v, want SOURCE_UNVERIFIED", err)
	}
}

func TestKAHContentDeduplicationIsSemanticAndLibraryScoped(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	firstLibrary, err := store.CreateLibrary(ctx, "KAH dedup A", "")
	if err != nil {
		t.Fatal(err)
	}
	secondLibrary, err := store.CreateLibrary(ctx, "KAH dedup B", "")
	if err != nil {
		t.Fatal(err)
	}
	payload := model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "可复用的事实", Description: "相同语义内容可以进入不同知识库。", Language: "zh-CN",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "主张", Content: "相同语义内容不应因 ID 或生成时间不同而变成新内容。"}},
	}
	first, _, err := store.CreateKnowledgeDraft(ctx, KnowledgeDraftInput{LibraryID: firstLibrary.ID, ClientSubmissionID: "dedup-1", Mode: "create", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	duplicatePayload := payload
	duplicatePayload.DuplicateIntent = "independent"
	second, _, err := store.CreateKnowledgeDraft(ctx, KnowledgeDraftInput{LibraryID: firstLibrary.ID, ClientSubmissionID: "dedup-2", Mode: "create", Payload: duplicatePayload})
	if err == nil || err.Error() != "EXACT_DUPLICATE" {
		t.Fatalf("same-library duplicate error = %v", err)
	}
	if second.Validation.ExactDuplicate != first.KnowledgeURI {
		t.Fatalf("duplicate URI = %q, want %q", second.Validation.ExactDuplicate, first.KnowledgeURI)
	}
	if _, _, err := store.CreateKnowledgeDraft(ctx, KnowledgeDraftInput{LibraryID: secondLibrary.ID, ClientSubmissionID: "dedup-3", Mode: "create", Payload: payload}); err != nil {
		t.Fatalf("same content in another library should be accepted: %v", err)
	}
}

func TestKAHSearchHonorsClassificationAndCursor(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	library, err := store.CreateLibrary(ctx, "KAH filters", "")
	if err != nil {
		t.Fatal(err)
	}
	for index, title := range []string{"筛选事实 A", "筛选事实 B"} {
		payload := model.KnowledgePayload{
			Schema: KAHKnowledgeSchema, Type: "claim", Title: title, Description: "可分页的事实", Language: "zh-CN",
			Classifications: map[string][]string{"domain": {"software"}}, Tags: []string{"filter"},
			Sections: []model.KnowledgeSection{{ID: "claim", Heading: "主张", Content: "筛选内容 " + string(rune('A'+index))}},
		}
		submission, _, createErr := store.CreateKnowledgeDraft(ctx, KnowledgeDraftInput{LibraryID: library.ID, ClientSubmissionID: "filter-" + string(rune('1'+index)), Mode: "create", Payload: payload})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = store.ReviewKAHSubmission(ctx, submission.ID, "tester", "approve", ""); createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = store.PublishKAHSubmission(ctx, submission.ID); createErr != nil {
			t.Fatal(createErr)
		}
	}
	first, err := store.SearchKnowledge(ctx, model.KnowledgeSearchRequest{LibraryIDs: []string{library.ID}, Query: "筛选", Classifications: map[string][]string{"domain": {"software"}}, Limit: 1})
	if err != nil || len(first.Results) != 1 || first.NextCursor == "" {
		t.Fatalf("first filtered page = %+v, err=%v", first, err)
	}
	second, err := store.SearchKnowledge(ctx, model.KnowledgeSearchRequest{LibraryIDs: []string{library.ID}, Query: "筛选", Classifications: map[string][]string{"domain": {"software"}}, Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Results) != 1 || second.Results[0].URI == first.Results[0].URI {
		t.Fatalf("second filtered page = %+v, err=%v", second, err)
	}
}

func TestKAHURIAndSemanticIdentityRules(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	if _, _, _, err := ParseKnowledgeURI(KnowledgeURI(id) + "?revision=1&revision=2"); err == nil {
		t.Fatal("duplicate revision query should be rejected")
	}
	if _, _, _, err := ParseKnowledgeURI(KnowledgeURI(id) + "?format=json"); err == nil {
		t.Fatal("unsupported query parameter should be rejected")
	}
	validation := ValidateKnowledgePayload(model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, Type: "claim", Title: "来源 URL", Description: "检查 HTTPS 来源格式。", Language: "zh-CN",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "主张", Content: "正文引用。[^source]"}},
		Sources:  []model.KnowledgeSource{{ID: "source", Resource: "https://"}},
	}, true)
	if validation.Valid {
		t.Fatal("malformed HTTPS source should fail validation")
	}

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	library, err := store.CreateLibrary(context.Background(), "KAH URI", "")
	if err != nil {
		t.Fatal(err)
	}
	payload := model.KnowledgePayload{
		Schema: KAHKnowledgeSchema, ID: KnowledgeURI(id), Type: "claim", Title: "规范化 ID", Description: "创建时保存无 revision 的 canonical URI。", Language: "zh-CN",
		Sections: []model.KnowledgeSection{{ID: "claim", Heading: "主张", Content: "规范化 ID 不带 revision。"}},
	}
	submission, _, err := store.CreateKnowledgeDraft(context.Background(), KnowledgeDraftInput{LibraryID: library.ID, ClientSubmissionID: "canonical-id", Mode: "create", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if submission.KnowledgeURI != KnowledgeURI(id) {
		t.Fatalf("knowledge URI = %q, want %q", submission.KnowledgeURI, KnowledgeURI(id))
	}
}

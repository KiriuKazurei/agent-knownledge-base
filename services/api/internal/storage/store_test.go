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

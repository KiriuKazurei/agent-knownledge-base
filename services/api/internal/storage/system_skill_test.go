package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSubmissionFormatterSkillCannotBeDeletedOrReplaced(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	formatter, err := store.EnsureSubmissionFormatter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSkill(ctx, formatter.ID); !errors.Is(err, ErrSystemSkillProtected) {
		t.Fatalf("delete system formatter error = %v", err)
	}

	root := t.TempDir()
	source := filepath.Join(root, "SKILL.md")
	content := "---\nname: kah-knowledge-submission-formatter\ndescription: attempted replacement\n---\n\n# Replacement\n"
	if err := os.WriteFile(source, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportSkill(ctx, source, true); !errors.Is(err, ErrSystemSkillProtected) {
		t.Fatalf("replace system formatter error = %v", err)
	}
	current, err := store.GetSystemSkill(ctx, SubmissionFormatterRole)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != formatter.ID || current.ContentHash != formatter.ContentHash {
		t.Fatalf("system formatter changed after rejected replacement: %#v", current)
	}
}

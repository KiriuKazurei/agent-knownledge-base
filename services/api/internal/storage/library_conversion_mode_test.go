package storage

import (
	"context"
	"testing"
)

func TestLibraryImportConversionModeKeepsLegacyBooleanCompatible(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	library, err := store.CreateLibrary(ctx, "转换模式", "")
	if err != nil {
		t.Fatal(err)
	}
	if library.ImportConversionMode != "reference" || library.AutoSummarizeImports {
		t.Fatalf("unexpected default conversion mode: %+v", library)
	}
	enhanced := "enhanced"
	updated, err := store.UpdateLibraryPatch(ctx, library.ID, LibraryPatch{ImportConversionMode: &enhanced})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ImportConversionMode != "enhanced" || !updated.AutoSummarizeImports {
		t.Fatalf("enhanced mode did not enable compatibility flag: %+v", updated)
	}
	off := "off"
	updated, err = store.UpdateLibraryPatch(ctx, library.ID, LibraryPatch{ImportConversionMode: &off})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ImportConversionMode != "off" || updated.AutoSummarizeImports {
		t.Fatalf("off mode did not disable compatibility flag: %+v", updated)
	}
	if _, err := store.UpdateLibraryPatch(ctx, library.ID, LibraryPatch{ImportConversionMode: stringPtr("invalid")}); err == nil {
		t.Fatal("invalid conversion mode should be rejected")
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE libraries SET auto_summarize_imports=1, import_conversion_mode='reference' WHERE id=?`, library.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.GetLibrary(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ImportConversionMode != "enhanced" {
		t.Fatalf("legacy auto summarize flag was not mapped: %+v", legacy)
	}
}

func stringPtr(value string) *string { return &value }

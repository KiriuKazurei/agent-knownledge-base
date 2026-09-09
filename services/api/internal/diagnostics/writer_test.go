package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotationAndCredentials(t *testing.T) {
	p := filepath.Join(t.TempDir(), "api.log")
	w := &Writer{Path: p, MaxBytes: 32}
	secret := "desktop_abcdefghijklmnopqrstuvwxyz123456"
	for i := 0; i < 8; i++ {
		if _, err := w.Write([]byte("Bearer " + secret + " operation failed with source ID abc\n")); err != nil {
			t.Fatal(err)
		}
	}
	for _, suffix := range []string{"", ".1", ".2", ".3"} {
		b, err := os.ReadFile(p + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), secret) || !strings.Contains(string(b), "operation failed") {
			t.Fatalf("invalid diagnostic content: %s", b)
		}
	}
	if _, err := os.Stat(p + ".4"); !os.IsNotExist(err) {
		t.Fatal("unbounded rotation")
	}
}

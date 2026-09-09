package diagnostics

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
)

// Writer keeps bounded JSONL logs and never retains authentication credentials.
type Writer struct {
	Path     string
	MaxBytes int64
	mu       sync.Mutex
}

var credentials = regexp.MustCompile(`\b(desktop_|kah_|sk-)[A-Za-z0-9_-]{16,}`)
var bearer = regexp.MustCompile(`(?i)(Bearer\s+)[^\s"',}]+`)

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(w.Path), 0700); err != nil {
		return 0, err
	}
	limit := w.MaxBytes
	if limit == 0 {
		limit = 5 * 1024 * 1024
	}
	if info, err := os.Stat(w.Path); err == nil && info.Size() >= limit {
		if err := os.Remove(w.Path + ".3"); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		for i := 2; i >= 1; i-- {
			if err := os.Rename(w.Path+"."+strconv.Itoa(i), w.Path+"."+strconv.Itoa(i+1)); err != nil && !os.IsNotExist(err) {
				return 0, err
			}
		}
		if err := os.Rename(w.Path, w.Path+".1"); err != nil {
			return 0, err
		}
	}
	file, err := os.OpenFile(w.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	safe := credentials.ReplaceAll(p, []byte("[REDACTED]"))
	safe = bearer.ReplaceAll(safe, []byte("${1}[REDACTED]"))
	if _, err := file.Write(safe); err != nil {
		return 0, err
	}
	return len(p), nil
}

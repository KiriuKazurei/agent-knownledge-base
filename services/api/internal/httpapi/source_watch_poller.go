package httpapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

const defaultSourceWatchPollInterval = 5 * time.Second

// StartSourceWatchers starts the local-first source monitor. It uses a
// bounded polling loop instead of exposing an OS-specific event API, so the
// same lifecycle works on supported Windows environments and survives worker
// restarts. The durable scan job remains the source of truth for recovery.
func (s *Server) StartSourceWatchers(ctx context.Context) {
	if s == nil || s.Store == nil {
		return
	}
	go s.runSourceWatchPoller(ctx, defaultSourceWatchPollInterval)
}

func (s *Server) runSourceWatchPoller(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = defaultSourceWatchPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	snapshots := map[string]string{}
	inFlight := map[string]bool{}
	done := make(chan string, 16)
	poll := func() {
		watches, err := s.Store.ListSourceWatches(ctx, "")
		if err != nil {
			s.logSourceWatch("list source watches", err)
			return
		}
		active := map[string]bool{}
		for _, watch := range watches {
			active[watch.ID] = true
			if !watch.Enabled {
				delete(snapshots, watch.ID)
				delete(inFlight, watch.ID)
				continue
			}
			fingerprint, fingerprintErr := sourceWatchFingerprint(watch)
			if fingerprintErr != nil {
				s.logSourceWatch("fingerprint source watch "+watch.ID, fingerprintErr)
				continue
			}
			previous, known := snapshots[watch.ID]
			if inFlight[watch.ID] {
				// Keep the old fingerprint while a scan is running. A change made
				// during that scan is then observed on the next poll.
				continue
			}
			if !known {
				snapshots[watch.ID] = fingerprint
				continue
			}
			if previous == fingerprint {
				continue
			}
			job, jobErr := s.Store.CreateJob(ctx, "source_scan", map[string]any{"watchId": watch.ID})
			if jobErr != nil {
				s.logSourceWatch("create source scan for "+watch.ID, jobErr)
				continue
			}
			snapshots[watch.ID] = fingerprint
			inFlight[watch.ID] = true
			go func(jobID string, item model.SourceWatch) {
				s.runSourceWatchScanContext(ctx, jobID, item)
				done <- item.ID
			}(job.ID, watch)
		}
		for id := range snapshots {
			if !active[id] {
				delete(snapshots, id)
				delete(inFlight, id)
			}
		}
	}
	poll()
	for {
		select {
		case <-ctx.Done():
			for len(inFlight) > 0 {
				id := <-done
				delete(inFlight, id)
			}
			return
		case id := <-done:
			delete(inFlight, id)
		case <-ticker.C:
			poll()
		}
	}
}

func (s *Server) logSourceWatch(action string, err error) {
	if s.Logger != nil {
		s.Logger.Warn(action, "error", err)
	}
}

func sourceWatchFingerprint(watch model.SourceWatch) (string, error) {
	entries := []string{}
	err := filepath.WalkDir(watch.RootPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != watch.RootPath && !watch.Recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !supportedImportPath(path) {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		relative, relativeErr := filepath.Rel(watch.RootPath, path)
		if relativeErr != nil {
			return relativeErr
		}
		entries = append(entries, fmt.Sprintf("%s|%d|%d", filepath.Clean(relative), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	return strings.Join(entries, "\n"), nil
}

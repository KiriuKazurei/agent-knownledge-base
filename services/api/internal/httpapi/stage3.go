package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/gin-gonic/gin"
)

func (s *Server) listFolders(c *gin.Context) {
	items, err := s.Store.ListFolders(operationContext(c), c.Query("libraryId"))
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) createFolder(c *gin.Context) {
	var input struct {
		LibraryID string `json:"libraryId"`
		Name      string `json:"name"`
		ParentID  string `json:"parentId"`
	}
	if !bind(c, &input) {
		return
	}
	item, err := s.Store.CreateFolder(operationContext(c), input.LibraryID, input.Name, input.ParentID)
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "parent_folder_not_found", "Parent folder not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusBadRequest, "folder_create_failed", err.Error(), false)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (s *Server) deleteFolder(c *gin.Context) {
	err := s.Store.DeleteFolder(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "folder_not_found", "Folder not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) assignDocumentFolder(c *gin.Context) {
	err := s.Store.SetDocumentFolder(operationContext(c), c.Param("id"), c.Param("folderId"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "document_or_folder_not_found", "Document or folder not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusBadRequest, "folder_assign_failed", err.Error(), false)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) removeDocumentFolder(c *gin.Context) {
	err := s.Store.RemoveDocumentFolder(operationContext(c), c.Param("id"), c.Param("folderId"))
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) listSourceWatches(c *gin.Context) {
	items, err := s.Store.ListSourceWatches(operationContext(c), c.Query("libraryId"))
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) createSourceWatch(c *gin.Context) {
	var input struct {
		LibraryID string `json:"libraryId"`
		RootPath  string `json:"rootPath"`
		Recursive *bool  `json:"recursive"`
	}
	if !bind(c, &input) {
		return
	}
	recursive := true
	if input.Recursive != nil {
		recursive = *input.Recursive
	}
	item, err := s.Store.CreateSourceWatch(operationContext(c), input.LibraryID, input.RootPath, recursive)
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "library_not_found", "Library not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusBadRequest, "watch_create_failed", err.Error(), false)
		return
	}
	job, err := s.Store.CreateJob(operationContext(c), "source_scan", map[string]any{"watchId": item.ID})
	if err != nil {
		_ = s.Store.DeleteSourceWatch(operationContext(c), item.ID)
		s.problem(c, http.StatusInternalServerError, "job_create_failed", err.Error(), true)
		return
	}
	go s.runSourceWatchScan(job.ID, item)
	c.JSON(http.StatusAccepted, gin.H{"watch": item, "job": job})
}

func (s *Server) scanSourceWatch(c *gin.Context) {
	item, err := s.Store.GetSourceWatch(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "watch_not_found", "Source watch not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	job, err := s.Store.CreateJob(operationContext(c), "source_scan", map[string]any{"watchId": item.ID})
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "job_create_failed", err.Error(), true)
		return
	}
	go s.runSourceWatchScan(job.ID, item)
	c.JSON(http.StatusAccepted, job)
}

func (s *Server) deleteSourceWatch(c *gin.Context) {
	err := s.Store.DeleteSourceWatch(operationContext(c), c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "watch_not_found", "Source watch not found", false)
		return
	}
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) runSourceWatchScan(jobID string, watch model.SourceWatch) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.05, "Scanning "+watch.RootPath)
	paths := []string{}
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
		if entry.Type().IsRegular() && supportedImportPath(path) {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		s.failJob(ctx, jobID, err)
		_ = s.Store.UpdateSourceWatchScan(ctx, watch.ID, err.Error())
		return
	}
	var scanErr error
	for index, path := range paths {
		child, childErr := s.Store.CreateJob(ctx, "file_import", map[string]any{"libraryId": watch.LibraryID, "path": path, "watchId": watch.ID, "name": filepath.Base(path)})
		if childErr != nil {
			if scanErr == nil {
				scanErr = fmt.Errorf("create import job for %s: %w", filepath.Base(path), childErr)
			}
		} else {
			s.runFileImport(child.ID, watch.LibraryID, path)
			jobs, listErr := s.Store.ListJobs(ctx)
			if listErr != nil {
				if scanErr == nil {
					scanErr = fmt.Errorf("read import job for %s: %w", filepath.Base(path), listErr)
				}
			} else {
				for _, item := range jobs {
					if item.ID == child.ID && item.Status != "completed" && scanErr == nil {
						scanErr = fmt.Errorf("import %s failed: %s", filepath.Base(path), item.Message)
						break
					}
				}
			}
		}
		_ = s.Store.UpdateJob(ctx, jobID, "running", 0.1+0.8*float64(index+1)/float64(maxInt(1, len(paths))), "Imported "+filepath.Base(path))
	}
	if scanErr != nil {
		_ = s.Store.UpdateSourceWatchScan(ctx, watch.ID, scanErr.Error())
		s.failJob(ctx, jobID, scanErr)
		return
	}
	message := "Scanned 0 files"
	if len(paths) > 0 {
		message = "Scanned " + itoa(len(paths)) + " files"
	}
	_ = s.Store.UpdateSourceWatchScan(ctx, watch.ID, message)
	if reconcileErr := s.reconcileSourceWatchAfterScan(ctx, watch); reconcileErr != nil {
		s.failJob(ctx, jobID, reconcileErr)
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, message)
}

func supportedImportPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown", ".txt", ".rst", ".csv", ".tsv", ".log", ".ini", ".conf", ".properties", ".html", ".htm", ".docx", ".xlsx", ".xlsm", ".pptx", ".pdf":
		return true
	default:
		return isCode(path)
	}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func (s *Server) runURLImportControlled(jobID, libraryID, target string, maxDepth, maxPages int) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	origin, err := url.Parse(target)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	queue := []string{target}
	seen := map[string]bool{target: true}
	depth := map[string]int{target: 0}
	imported := 0
	for len(queue) > 0 && imported < maxPages {
		current := queue[0]
		queue = queue[1:]
		if !sameHost(origin, current) || !robotsAllowed(ctx, current) {
			continue
		}
		body, contentType, err := fetchPageWithRetry(ctx, current)
		if err != nil {
			s.failJob(ctx, jobID, err)
			return
		}
		relative, digest, err := s.Store.PutObject(bytes.NewReader(body))
		if err != nil {
			s.failJob(ctx, jobID, err)
			return
		}
		_, duplicateErr := s.Store.FindDocumentByHash(ctx, libraryID, digest)
		if duplicateErr == nil {
			imported++
			continue
		}
		if !errors.Is(duplicateErr, sql.ErrNoRows) {
			s.failJob(ctx, jobID, duplicateErr)
			return
		}
		page, _ := url.Parse(current)
		title := page.Host + page.Path
		if title == page.Host {
			title += "/"
		}
		doc, err := s.Store.CreatePendingDocument(ctx, libraryID, title, contentType, "", current, relative, digest)
		if err != nil {
			s.failJob(ctx, jobID, err)
			return
		}
		resolved, err := s.Store.Resolve(relative)
		if err != nil {
			_ = s.Store.FailDocument(ctx, doc.ID, err)
			s.failJob(ctx, jobID, err)
			return
		}
		chunks, err := s.parseDocument(ctx, doc, resolved)
		if err != nil {
			_ = s.Store.FailDocument(ctx, doc.ID, err)
			s.failJob(ctx, jobID, err)
			return
		}
		if err := s.Store.ReplaceChunks(ctx, doc.ID, chunks); err != nil {
			_ = s.Store.FailDocument(ctx, doc.ID, err)
			s.failJob(ctx, jobID, err)
			return
		}
		if s.Worker != nil {
			if err := s.Worker.Call(ctx, "index_upsert", map[string]any{"libraryId": libraryID, "documentId": doc.ID, "chunks": chunks}, nil); err != nil {
				_ = s.Store.FailDocument(ctx, doc.ID, err)
				s.failJob(ctx, jobID, err)
				return
			}
		}
		imported++
		_ = s.Store.UpdateJob(ctx, jobID, "running", float64(imported)/float64(maxPages), "Imported "+title)
		if depth[current] < maxDepth && strings.Contains(strings.ToLower(contentType), "html") {
			for _, link := range extractPageLinks(current, body) {
				if !seen[link] && sameHost(origin, link) && len(queue)+imported < maxPages*2 {
					seen[link] = true
					depth[link] = depth[current] + 1
					queue = append(queue, link)
				}
			}
		}
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Imported "+itoa(imported)+" page(s)")
}

func fetchPageWithRetry(ctx context.Context, address string) ([]byte, string, error) {
	var last error
	client := &http.Client{Timeout: 20 * time.Second}
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err == nil {
			request.Header.Set("User-Agent", "KnowledgeAgentHub/0.1")
			response, requestErr := client.Do(request)
			if requestErr == nil {
				data, readErr := io.ReadAll(io.LimitReader(response.Body, 20*1024*1024+1))
				response.Body.Close()
				if readErr == nil && response.StatusCode/100 == 2 && len(data) <= 20*1024*1024 {
					mediaType := response.Header.Get("Content-Type")
					if index := strings.IndexByte(mediaType, ';'); index >= 0 {
						mediaType = mediaType[:index]
					}
					if mediaType == "" {
						mediaType = "text/html"
					}
					return data, mediaType, nil
				}
				if readErr != nil {
					last = readErr
				} else {
					last = errors.New("source returned " + response.Status)
				}
			} else {
				last = requestErr
			}
		} else {
			last = err
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
		}
	}
	return nil, "", last
}

func robotsAllowed(ctx context.Context, address string) bool {
	parsed, err := url.Parse(address)
	if err != nil {
		return false
	}
	robotsURL := parsed.Scheme + "://" + parsed.Host + "/robots.txt"
	data, _, err := fetchPageWithRetry(ctx, robotsURL)
	if err != nil {
		return true
	}
	userAgentAll := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.ToLower(line))
		if strings.HasPrefix(line, "user-agent:") {
			userAgentAll = strings.TrimSpace(strings.TrimPrefix(line, "user-agent:")) == "*"
		}
		if userAgentAll && strings.HasPrefix(line, "disallow:") {
			blocked := strings.TrimSpace(strings.TrimPrefix(line, "disallow:"))
			if blocked != "" && strings.HasPrefix(parsed.EscapedPath(), blocked) {
				return false
			}
		}
	}
	return true
}
func sameHost(origin *url.URL, address string) bool {
	parsed, err := url.Parse(address)
	return err == nil && parsed.Scheme == origin.Scheme && strings.EqualFold(parsed.Host, origin.Host)
}

func extractPageLinks(base string, body []byte) []string {
	pattern := regexp.MustCompile("(?i)href\\s*=\\s*[\\\"']([^\\\"']+)")
	matches := pattern.FindAllSubmatch(body, 100)
	result := []string{}
	baseURL, _ := url.Parse(base)
	for _, match := range matches {
		parsed, err := url.Parse(strings.TrimSpace(string(match[1])))
		if err != nil {
			continue
		}
		resolved := baseURL.ResolveReference(parsed)
		resolved.Fragment = ""
		if resolved.Scheme == "http" || resolved.Scheme == "https" {
			result = append(result, resolved.String())
		}
	}
	return result
}
func (s *Server) putFileWithRetry(ctx context.Context, path string) (string, string, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		file, err := os.Open(path)
		if err == nil {
			relative, digest, putErr := s.Store.PutObject(file)
			closeErr := file.Close()
			if putErr == nil {
				putErr = closeErr
			}
			if putErr == nil {
				return relative, digest, nil
			}
			err = putErr
		}
		last = err
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", "", ctx.Err()
			case <-timer.C:
			}
		}
	}
	return "", "", last
}
func (s *Server) rebuildIndex(c *gin.Context) {
	var input struct {
		LibraryID string `json:"libraryId"`
	}
	if !bind(c, &input) {
		return
	}
	if input.LibraryID == "" {
		s.problem(c, http.StatusBadRequest, "invalid_rebuild", "libraryId is required", false)
		return
	}
	if s.Worker == nil || s.Worker.State() != "ok" {
		s.problem(c, http.StatusServiceUnavailable, "worker_unavailable", "Index worker is unavailable", true)
		return
	}
	job, err := s.Store.CreateJob(operationContext(c), "index_rebuild", input)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "job_create_failed", err.Error(), true)
		return
	}
	go s.runIndexRebuild(job.ID, input.LibraryID)
	c.JSON(http.StatusAccepted, job)
}

func (s *Server) runIndexRebuild(jobID, libraryID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.1, "Loading authoritative chunks")
	chunks, err := s.Store.ChunksForLibrary(ctx, libraryID)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.35, "Building a new index version")
	result := map[string]any{}
	if err := s.Worker.Call(ctx, "index_rebuild", map[string]any{"libraryId": libraryID, "chunks": chunks}, &result); err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Index version switched")
}
func (s *Server) syncExistingSourceFile(ctx context.Context, jobID, libraryID, path, relative, digest string) (bool, error) {
	existing, err := s.Store.FindDocumentBySourcePath(ctx, libraryID, path)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if existing.ContentHash == digest && existing.Status == "ready" {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Deduplicated "+existing.Title)
		return true, nil
	}
	if err := s.Store.UpdateDocumentContent(ctx, existing.ID, relative, digest); err != nil {
		return true, err
	}
	resolved, err := s.Store.Resolve(relative)
	if err != nil {
		return true, err
	}
	chunks, err := s.parseDocument(ctx, existing, resolved)
	if err != nil {
		_ = s.Store.FailDocument(ctx, existing.ID, err)
		return true, err
	}
	for i := range chunks {
		chunks[i].ContentHash = digest
	}
	if err := s.Store.ReplaceChunks(ctx, existing.ID, chunks); err != nil {
		return true, err
	}
	if s.Worker != nil {
		if err := s.Worker.Call(ctx, "index_upsert", map[string]any{"libraryId": libraryID, "documentId": existing.ID, "chunks": chunks}, nil); err != nil {
			_ = s.Store.FailDocument(ctx, existing.ID, err)
			return true, err
		}
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Updated "+existing.Title)
	return true, nil
}

func (s *Server) restoreRenamedSource(ctx context.Context, jobID, path string, existing model.Document) (bool, error) {
	if existing.SourcePath == "" || filepath.Clean(existing.SourcePath) == filepath.Clean(path) {
		return false, nil
	}
	if _, err := os.Stat(existing.SourcePath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return true, err
		}
	} else {
		return false, nil
	}
	if err := s.Store.UpdateDocumentSourcePath(ctx, existing.ID, path); err != nil {
		return true, err
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "Renamed "+existing.Title)
	return true, nil
}

func pathWithinRoot(root, target string) bool {
	rootAbs, rootErr := filepath.Abs(filepath.Clean(root))
	targetAbs, targetErr := filepath.Abs(filepath.Clean(target))
	if rootErr != nil || targetErr != nil {
		return false
	}
	relative, err := filepath.Rel(rootAbs, targetAbs)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *Server) reconcileSourceWatchAfterScan(ctx context.Context, watch model.SourceWatch) error {
	if !watch.Enabled {
		return nil
	}
	seen := map[string]bool{}
	if err := filepath.WalkDir(watch.RootPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != watch.RootPath && !watch.Recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() && supportedImportPath(path) {
			seen[filepath.Clean(path)] = true
		}
		return nil
	}); err != nil {
		return err
	}
	documents, err := s.Store.ListDocumentsWithSources(ctx, watch.LibraryID)
	if err != nil {
		return err
	}
	missing := 0
	for _, document := range documents {
		if !pathWithinRoot(watch.RootPath, document.SourcePath) || seen[filepath.Clean(document.SourcePath)] || document.Status == "source_missing" {
			continue
		}
		if s.Worker != nil {
			if err := s.Worker.Call(ctx, "index_delete", map[string]any{"libraryId": document.LibraryID, "documentId": document.ID}, nil); err != nil {
				return err
			}
		}
		if err := s.Store.MarkDocumentSourceMissing(ctx, document.ID); err != nil {
			return err
		}
		missing++
	}
	if missing > 0 {
		_ = s.Store.UpdateSourceWatchScan(ctx, watch.ID, "Marked "+itoa(missing)+" missing source(s)")
	}
	return nil
}
func (s *Server) authorizeSkill(c *gin.Context, skillID string) bool {
	value, _ := c.Get("identity")
	id, _ := value.(identity)
	if id.Desktop || len(id.Token.LibraryIDs) == 0 {
		return true
	}
	if _, err := s.Store.GetSkill(operationContext(c), skillID); errors.Is(err, sql.ErrNoRows) {
		s.problem(c, http.StatusNotFound, "skill_not_found", "Skill not found", false)
		return false
	} else if err != nil {
		s.problem(c, http.StatusInternalServerError, "database_error", err.Error(), true)
		return false
	}
	accessible, err := s.Store.SkillAccessibleToLibraries(operationContext(c), skillID, id.Token.LibraryIDs)
	if err != nil {
		s.problem(c, http.StatusInternalServerError, "skill_access_check_failed", err.Error(), true)
		return false
	}
	if !accessible {
		s.problem(c, http.StatusForbidden, "skill_forbidden", "Token cannot access this Skill", false)
		return false
	}
	return true
}

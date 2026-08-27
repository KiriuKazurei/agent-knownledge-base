package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
)

// ResumeQueuedJobs replays durable jobs left queued by startup migration. Job
// payloads stay inside storage and are never exposed through the jobs API.
func (s *Server) ResumeQueuedJobs(ctx context.Context) error {
	items, err := s.Store.ListQueuedJobs(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		claimed, claimErr := s.Store.ClaimJob(ctx, item.ID)
		if claimErr != nil {
			return claimErr
		}
		if !claimed {
			continue
		}
		if item.PayloadError != nil {
			s.failJob(context.Background(), item.ID, item.PayloadError)
			continue
		}
		if err := s.resumeQueuedJob(item); err != nil {
			s.failJob(context.Background(), item.ID, err)
		}
	}
	return nil
}

func (s *Server) resumeQueuedJob(item storage.QueuedJob) error {
	payload := item.Payload
	switch item.Kind {
	case "file_import":
		libraryID, err := requiredJobString(payload, "libraryId")
		if err != nil {
			return err
		}
		path, err := requiredJobString(payload, "path")
		if err != nil {
			return err
		}
		go s.runFileImport(item.ID, libraryID, path)
	case "url_import":
		libraryID, err := requiredJobString(payload, "libraryId")
		if err != nil {
			return err
		}
		target, err := requiredJobString(payload, "url")
		if err != nil {
			return err
		}
		maxDepth, err := optionalJobInt(payload, "maxDepth", 0)
		if err != nil {
			return err
		}
		maxPages, err := optionalJobInt(payload, "maxPages", 1)
		if err != nil {
			return err
		}
		if maxDepth < 0 || maxDepth > 3 || maxPages < 1 || maxPages > 100 {
			return errors.New("persisted crawl limits are invalid")
		}
		go s.runURLImport(item.ID, libraryID, target, maxDepth, maxPages)
	case "skill_import":
		path, err := requiredJobString(payload, "path")
		if err != nil {
			return err
		}
		replace, err := optionalJobBool(payload, "replace", false)
		if err != nil {
			return err
		}
		go s.runSkillImport(item.ID, path, replace)
	case "source_scan":
		watchID, err := requiredJobString(payload, "watchId")
		if err != nil {
			return err
		}
		watch, err := s.Store.GetSourceWatch(context.Background(), watchID)
		if err != nil {
			return fmt.Errorf("load source watch %s: %w", watchID, err)
		}
		go s.runSourceWatchScan(item.ID, watch)
	case "index_rebuild":
		libraryID, err := requiredJobString(payload, "libraryId")
		if err != nil {
			return err
		}
		go s.runIndexRebuild(item.ID, libraryID)
	case "knowledge_review":
		submissionID, err := requiredJobString(payload, "submissionId")
		if err != nil {
			return err
		}
		go s.runKnowledgeReview(item.ID, submissionID)
	case "knowledge_publish":
		submissionID, err := requiredJobString(payload, "submissionId")
		if err != nil {
			return err
		}
		go s.runKnowledgePublish(item.ID, submissionID)
	default:
		return fmt.Errorf("unsupported persisted job kind %q", item.Kind)
	}
	return nil
}

func requiredJobString(payload map[string]any, key string) (string, error) {
	value, ok := payload[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("persisted job field %q is required", key)
	}
	return value, nil
}

func optionalJobInt(payload map[string]any, key string, fallback int) (int, error) {
	value, ok := payload[key]
	if !ok {
		return fallback, nil
	}
	switch number := value.(type) {
	case float64:
		if number != float64(int(number)) {
			return 0, fmt.Errorf("persisted job field %q must be an integer", key)
		}
		return int(number), nil
	case int:
		return number, nil
	default:
		return 0, fmt.Errorf("persisted job field %q must be an integer", key)
	}
}

func optionalJobBool(payload map[string]any, key string, fallback bool) (bool, error) {
	value, ok := payload[key]
	if !ok {
		return fallback, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("persisted job field %q must be a boolean", key)
	}
	return result, nil
}

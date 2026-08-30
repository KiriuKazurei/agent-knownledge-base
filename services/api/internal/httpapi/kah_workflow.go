package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/secrets"
)

// runKAHKnowledgeReview is the KAH v1 automatic review worker. It is kept
// separate from the legacy Agent Markdown reviewer because the two workflows
// persist different submission IDs, revision state, and review records.
func (s *Server) runKAHKnowledgeReview(jobID, submissionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.1, "Loading KAH draft and review policy")
	claimed, err := s.Store.MarkKAHSubmissionReviewing(ctx, submissionID)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if !claimed {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "KAH submission was already reviewed")
		return
	}
	fail := func(cause error) {
		_ = s.Store.ResetKAHSubmissionReview(context.Background(), submissionID)
		s.failJob(context.Background(), jobID, cause)
	}
	item, err := s.Store.GetKAHSubmission(ctx, submissionID)
	if err != nil {
		fail(err)
		return
	}
	library, ok, err := s.findLibrary(ctx, item.LibraryID)
	if err != nil || !ok || !library.AutoReviewAgentSubmissions || strings.TrimSpace(library.ReviewProviderID) == "" {
		fail(errors.New("automatic KAH review is not configured for this library"))
		return
	}
	provider, err := s.Store.GetProvider(ctx, library.ReviewProviderID)
	if err != nil {
		fail(err)
		return
	}
	if !provider.Local {
		allowed, allowErr := s.Store.LibrariesAllowRemote(ctx, []string{library.ID})
		if allowErr != nil {
			fail(allowErr)
			return
		}
		if !allowed {
			fail(errors.New("remote review is not allowed for this library"))
			return
		}
	}
	evidence, err := s.Store.Search(ctx, model.QueryRequest{Query: item.Title + " " + item.Summary, LibraryIDs: []string{item.LibraryID}, TopK: 8})
	if err != nil {
		fail(err)
		return
	}
	key, err := secrets.Get("provider:" + provider.ID)
	if err != nil {
		fail(err)
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.45, "Calling configured KAH review model")
	raw, err := s.Providers.Review(ctx, provider, key, item.Markdown, evidence)
	if err != nil {
		fail(err)
		return
	}
	var output reviewModelOutput
	decoder := json.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(&output); err != nil || (output.Decision != "approve" && output.Decision != "reject" && output.Decision != "needs_human") {
		if err == nil {
			err = errors.New("review model returned an invalid decision")
		}
		fail(err)
		return
	}
	issues := make([]string, 0, len(output.Issues))
	for _, issue := range output.Issues {
		issues = append(issues, issue.Severity+":"+issue.Code+":"+issue.Message)
	}
	if output.Decision == "approve" && (output.Confidence < 0.85 || hasBlockingReviewIssue(output.Issues)) {
		output.Decision = "needs_human"
		output.Reason = "Model confidence or issue severity requires human review: " + output.Reason
	}
	changed, err := s.Store.RecordKAHSubmissionReview(ctx, submissionID, provider.ID, output.Decision, output.Reason)
	if err != nil {
		fail(err)
		return
	}
	if !changed {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "KAH submission was reviewed by another actor")
		return
	}
	if output.Decision == "approve" {
		publish, publishErr := s.Store.CreateJob(ctx, "kah_knowledge_publish", map[string]any{"submissionId": submissionID})
		if publishErr != nil {
			fail(publishErr)
			return
		}
		go s.runKAHKnowledgePublish(publish.ID, submissionID)
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "KAH review decision: "+output.Decision)
}

func (s *Server) runKAHKnowledgePublish(jobID, submissionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_ = s.Store.UpdateJob(ctx, jobID, "running", 0.2, "Publishing approved KAH knowledge")
	item, err := s.Store.GetKAHSubmission(ctx, submissionID)
	if err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	if item.ReviewStatus == "published" {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "KAH knowledge was already published")
		return
	}
	if item.ReviewStatus != "approved_pending_index" {
		_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "KAH submission is not approved for publication")
		return
	}
	if _, err = s.Store.PublishKAHSubmission(ctx, submissionID); err != nil {
		s.failJob(ctx, jobID, err)
		return
	}
	_ = s.Store.UpdateJob(ctx, jobID, "completed", 1, "KAH knowledge published to formal revisions")
}

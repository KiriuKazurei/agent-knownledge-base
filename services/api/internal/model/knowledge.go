package model

import "time"

// KnowledgePayload is the canonical KAH Knowledge Profile v1 representation.
// Workflow state, hashes, and trust flags are deliberately kept outside this
// immutable semantic payload.
type KnowledgePayload struct {
	Schema          string                  `json:"schema"`
	ID              string                  `json:"id,omitempty"`
	Revision        int                     `json:"revision,omitempty"`
	Type            string                  `json:"type"`
	Subtype         string                  `json:"subtype,omitempty"`
	Title           string                  `json:"title"`
	Description     string                  `json:"description"`
	Language        string                  `json:"language"`
	Aliases         []string                `json:"aliases,omitempty"`
	PrimaryPath     []string                `json:"primary_path,omitempty"`
	Classifications map[string][]string     `json:"classifications,omitempty"`
	Tags            []string                `json:"tags,omitempty"`
	Sections        []KnowledgeSection      `json:"sections"`
	Sources         []KnowledgeSource       `json:"sources,omitempty"`
	Relations       []KnowledgeRelation     `json:"relations,omitempty"`
	Generated       KnowledgeGenerated      `json:"generated,omitempty"`
	Verified        []KnowledgeVerification `json:"verified,omitempty"`
	StaleAfter      *time.Time              `json:"stale_after,omitempty"`
	Derivation      *KnowledgeDerivation    `json:"derivation,omitempty"`
	DuplicateIntent string                  `json:"duplicate_intent,omitempty"`
}

type KnowledgeSection struct {
	ID      string `json:"id"`
	Heading string `json:"heading"`
	Content string `json:"content"`
}

type KnowledgeSource struct {
	ID       string                  `json:"id"`
	Resource string                  `json:"resource"`
	Title    string                  `json:"title,omitempty"`
	Locator  map[string]any          `json:"locator,omitempty"`
	Snapshot KnowledgeSourceSnapshot `json:"snapshot,omitempty"`
}

type KnowledgeSourceSnapshot struct {
	Status      string    `json:"status,omitempty"`
	ContentHash string    `json:"content_hash,omitempty"`
	CapturedAt  time.Time `json:"captured_at,omitempty"`
}

type KnowledgeRelation struct {
	Type           string `json:"type"`
	Target         string `json:"target"`
	TargetRevision int    `json:"target_revision,omitempty"`
}

type KnowledgeGenerated struct {
	By string    `json:"by,omitempty"`
	At time.Time `json:"at,omitempty"`
}

type KnowledgeVerification struct {
	By     string    `json:"by"`
	At     time.Time `json:"at"`
	Method string    `json:"method,omitempty"`
}

type KnowledgeDerivation struct {
	ID          string   `json:"id,omitempty"`
	Premises    []string `json:"premises,omitempty"`
	Method      string   `json:"method,omitempty"`
	Steps       []string `json:"steps,omitempty"`
	Conclusion  string   `json:"conclusion,omitempty"`
	Limitations string   `json:"limitations,omitempty"`
	Uncertainty string   `json:"uncertainty,omitempty"`
}

type KnowledgeRevision struct {
	LibraryID   string           `json:"libraryId"`
	KnowledgeID string           `json:"knowledgeId"`
	URI         string           `json:"uri"`
	Revision    int              `json:"revision"`
	Payload     KnowledgePayload `json:"payload"`
	Markdown    string           `json:"markdown"`
	ContentHash string           `json:"contentHash"`
	Status      string           `json:"status"`
	Flags       []string         `json:"flags"`
	Stable      bool             `json:"stable"`
	CreatedAt   time.Time        `json:"createdAt"`
	SubmittedBy string           `json:"submittedBy,omitempty"`
}

type KnowledgeDirectoryEntry struct {
	URI               string              `json:"uri"`
	Revision          int                 `json:"revision"`
	Title             string              `json:"title"`
	Description       string              `json:"description"`
	Type              string              `json:"type"`
	Subtype           string              `json:"subtype,omitempty"`
	Language          string              `json:"language"`
	PrimaryPath       []string            `json:"primaryPath"`
	Classifications   map[string][]string `json:"classifications,omitempty"`
	Tags              []string            `json:"tags"`
	MatchedSectionIDs []string            `json:"matchedSectionIds"`
	MatchReason       string              `json:"matchReason"`
	Flags             []string            `json:"flags"`
	Trust             string              `json:"trust"`
	ConflictURIs      []string            `json:"conflictUris,omitempty"`
}

type KnowledgeSearchRequest struct {
	Query           string              `json:"query"`
	LibraryIDs      []string            `json:"libraryIds,omitempty"`
	Types           []string            `json:"types,omitempty"`
	Language        string              `json:"language,omitempty"`
	Classifications map[string][]string `json:"classifications,omitempty"`
	Tags            []string            `json:"tags,omitempty"`
	Statuses        []string            `json:"statuses,omitempty"`
	Limit           int                 `json:"limit,omitempty"`
	Cursor          string              `json:"cursor,omitempty"`
}

type KnowledgeSearchResponse struct {
	Results    []KnowledgeDirectoryEntry `json:"results"`
	NextCursor string                    `json:"nextCursor,omitempty"`
}

type KnowledgeValidationIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type KnowledgeValidation struct {
	Valid          bool                       `json:"valid"`
	Normalized     KnowledgePayload           `json:"normalized,omitempty"`
	Errors         []KnowledgeValidationIssue `json:"errors"`
	Warnings       []KnowledgeValidationIssue `json:"warnings"`
	ExactDuplicate string                     `json:"exactDuplicate,omitempty"`
	NearDuplicates []KnowledgeDirectoryEntry  `json:"nearDuplicates,omitempty"`
}

type KAHSubmission struct {
	ID                 string              `json:"id"`
	LibraryID          string              `json:"libraryId"`
	KnowledgeURI       string              `json:"knowledgeUri"`
	Revision           int                 `json:"revision"`
	Mode               string              `json:"mode"`
	ReviewStatus       string              `json:"reviewStatus"`
	Validation         KnowledgeValidation `json:"validation"`
	Title              string              `json:"title"`
	Summary            string              `json:"summary"`
	Tags               []string            `json:"tags"`
	Provenance         map[string]any      `json:"provenance"`
	Markdown           string              `json:"markdown,omitempty"`
	Reviews            []KAHReviewRecord   `json:"reviews,omitempty"`
	SubmittedByTokenID string              `json:"submittedByTokenId,omitempty"`
	ClientSubmissionID string              `json:"clientSubmissionId"`
	CreatedAt          time.Time           `json:"createdAt"`
	UpdatedAt          time.Time           `json:"updatedAt"`
}

type KAHReviewRecord struct {
	ID           string    `json:"id"`
	SubmissionID string    `json:"submissionId"`
	ReviewerType string    `json:"reviewerType"`
	Reviewer     string    `json:"reviewer"`
	Decision     string    `json:"decision"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"createdAt"`
}

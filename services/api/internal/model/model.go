package model

import "time"

type Library struct {
	ID                         string     `json:"id"`
	Name                       string     `json:"name"`
	Description                string     `json:"description"`
	Icon                       string     `json:"icon"`
	Color                      string     `json:"color"`
	Tags                       []string   `json:"tags"`
	Favorite                   bool       `json:"favorite"`
	AllowRemoteModels          bool       `json:"allowRemoteModels"`
	AutoSummarizeImports       bool       `json:"autoSummarizeImports"`
	ImportConversionMode       string     `json:"importConversionMode"`
	SummaryProviderID          string     `json:"summaryProviderId,omitempty"`
	AutoReviewAgentSubmissions bool       `json:"autoReviewAgentSubmissions"`
	ReviewProviderID           string     `json:"reviewProviderId,omitempty"`
	CreatedAt                  time.Time  `json:"createdAt"`
	UpdatedAt                  time.Time  `json:"updatedAt"`
	DeletedAt                  *time.Time `json:"deletedAt,omitempty"`
}

type Document struct {
	ID          string     `json:"id"`
	LibraryID   string     `json:"libraryId"`
	Title       string     `json:"title"`
	MediaType   string     `json:"mediaType"`
	SourcePath  string     `json:"sourcePath,omitempty"`
	SourceURL   string     `json:"sourceUrl,omitempty"`
	ObjectPath  string     `json:"objectPath,omitempty"`
	ContentHash string     `json:"contentHash,omitempty"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	Tags        []string   `json:"tags"`
	FolderIDs   []string   `json:"folderIds"`
	Favorite    bool       `json:"favorite"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	DeletedAt   *time.Time `json:"deletedAt,omitempty"`
}

type DocumentDetail struct {
	Document
	Preview []Chunk `json:"preview"`
}

type Skill struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	Compatibility      string            `json:"compatibility,omitempty"`
	License            string            `json:"license,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	AllowedTools       string            `json:"allowedTools,omitempty"`
	RootPath           string            `json:"rootPath"`
	EntryPoint         string            `json:"entryPoint"`
	ContentHash        string            `json:"contentHash"`
	Status             string            `json:"status"`
	Error              string            `json:"error,omitempty"`
	SystemRole         string            `json:"systemRole,omitempty"`
	FileCount          int               `json:"fileCount"`
	UsesLibraryIDs     []string          `json:"usesLibraryIds"`
	RequiresLibraryIDs []string          `json:"requiresLibraryIds"`
	CreatedAt          time.Time         `json:"createdAt"`
	UpdatedAt          time.Time         `json:"updatedAt"`
}

type SkillFile struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"mediaType"`
	URL       string `json:"url,omitempty"`
	Content   string `json:"content,omitempty"`
}

type SkillLink struct {
	SkillID   string `json:"skillId"`
	LibraryID string `json:"libraryId"`
	Relation  string `json:"relation"`
}

type SkillMatch struct {
	Skill
	Score float64 `json:"score"`
}

type SkillQueryRequest struct {
	Query      string   `json:"query" binding:"required"`
	LibraryIDs []string `json:"libraryIds"`
	TopK       int      `json:"topK"`
}

func (q *SkillQueryRequest) Defaults() {
	if q.TopK == 0 {
		q.TopK = 10
	}
}

type SkillQueryResponse struct {
	RequestID string       `json:"requestId"`
	Skills    []SkillMatch `json:"skills"`
}

type SkillManifest struct {
	Skill      Skill       `json:"skill"`
	Root       string      `json:"root"`
	EntryPoint SkillFile   `json:"entryPoint"`
	Files      []SkillFile `json:"files"`
}

type SubmissionFormatter struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	ContentHash string   `json:"contentHash"`
	Content     string   `json:"content"`
	Files       []string `json:"files"`
}

type SubmissionConstraints struct {
	MaxBytes            int      `json:"maxBytes"`
	RequiredFrontmatter []string `json:"requiredFrontmatter"`
	RequiredSections    []string `json:"requiredSections"`
}

type SubmissionPreparation struct {
	Ticket      string                `json:"ticket"`
	ExpiresAt   time.Time             `json:"expiresAt"`
	Formatter   SubmissionFormatter   `json:"formatter"`
	Constraints SubmissionConstraints `json:"constraints"`
}

type Chunk struct {
	ID          string         `json:"id"`
	DocumentID  string         `json:"documentId"`
	Text        string         `json:"text"`
	Location    map[string]any `json:"location"`
	ContentHash string         `json:"contentHash"`
}

type Scores struct {
	Lexical float64 `json:"lexical"`
	Vector  float64 `json:"vector"`
	Fusion  float64 `json:"fusion"`
	Final   float64 `json:"final"`
}

type Evidence struct {
	Chunk
	LibraryID string `json:"libraryId"`
	Title     string `json:"title"`
	Scores    Scores `json:"scores"`
}

type QueryRequest struct {
	Query         string   `json:"query" binding:"required"`
	LibraryIDs    []string `json:"libraryIds"`
	Tags          []string `json:"tags"`
	TopK          int      `json:"topK"`
	RetrievalMode string   `json:"retrievalMode"`
	ResponseMode  string   `json:"responseMode"`
	ProviderID    string   `json:"providerId"`
}

func (q *QueryRequest) Defaults() {
	if q.TopK == 0 {
		q.TopK = 10
	}
	if q.RetrievalMode == "" {
		q.RetrievalMode = "hybrid"
	}
	if q.ResponseMode == "" {
		q.ResponseMode = "evidence"
	}
}

type QueryResponse struct {
	RequestID      string     `json:"requestId"`
	Evidence       []Evidence `json:"evidence"`
	RequiredSkills []Skill    `json:"requiredSkills,omitempty"`
	Answer         string     `json:"answer,omitempty"`
	Degraded       bool       `json:"degraded"`
	DegradedReason string     `json:"degradedReason,omitempty"`
}

type Job struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Status         string    `json:"status"`
	Progress       float64   `json:"progress"`
	Message        string    `json:"message,omitempty"`
	Stage          string    `json:"stage,omitempty"`
	Outcome        string    `json:"outcome,omitempty"`
	CompletedUnits int       `json:"completedUnits,omitempty"`
	TotalUnits     int       `json:"totalUnits,omitempty"`
	ErrorCode      string    `json:"errorCode,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	OriginJobID    string    `json:"originJobId,omitempty"`
	CanCancel      bool      `json:"canCancel"`
	CanRetry       bool      `json:"canRetry"`
}

type ReviewRecord struct {
	ID           string    `json:"id"`
	ReviewerType string    `json:"reviewerType"`
	Reviewer     string    `json:"reviewer"`
	Decision     string    `json:"decision"`
	Confidence   float64   `json:"confidence,omitempty"`
	Reason       string    `json:"reason"`
	Issues       []string  `json:"issues"`
	CreatedAt    time.Time `json:"createdAt"`
}

type KnowledgeSubmission struct {
	ID                     string         `json:"id"`
	DocumentID             string         `json:"documentId"`
	LibraryID              string         `json:"libraryId"`
	Title                  string         `json:"title"`
	Summary                string         `json:"summary,omitempty"`
	Tags                   []string       `json:"tags,omitempty"`
	Provenance             map[string]any `json:"provenance,omitempty"`
	ContentHash            string         `json:"contentHash"`
	Status                 string         `json:"status"`
	ReviewStatus           string         `json:"reviewStatus"`
	FormatterSkillID       string         `json:"formatterSkillId"`
	FormatterSkillHash     string         `json:"formatterSkillHash"`
	SupersedesSubmissionID string         `json:"supersedesSubmissionId,omitempty"`
	ReviewJobID            string         `json:"reviewJobId,omitempty"`
	ReviewError            string         `json:"reviewError,omitempty"`
	SubmittedAt            time.Time      `json:"submittedAt"`
	UpdatedAt              time.Time      `json:"updatedAt"`
	Markdown               string         `json:"markdown,omitempty"`
	Document               *Document      `json:"document,omitempty"`
	Reviews                []ReviewRecord `json:"reviews,omitempty"`
}

type AgentToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	LibraryIDs []string   `json:"libraryIds"`
	Secret     string     `json:"secret,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

type AuditEntry struct {
	ID        string         `json:"id"`
	Actor     string         `json:"actor"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	Details   map[string]any `json:"details"`
	CreatedAt time.Time      `json:"createdAt"`
}

type Provider struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	BaseURL        string `json:"baseUrl"`
	Model          string `json:"model"`
	EmbeddingModel string `json:"embeddingModel"`
	Local          bool   `json:"local"`
	APIKey         string `json:"apiKey,omitempty"`
}

type SavedSearch struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Query      string             `json:"query"`
	View       string             `json:"view"`
	LibraryIDs []string           `json:"libraryIds"`
	Tags       []string           `json:"tags"`
	Filters    SavedSearchFilters `json:"filters"`
	Sort       SavedSearchSort    `json:"sort"`
}

type SavedSearchFilters struct {
	Tags          []string `json:"tags,omitempty"`
	Favorite      bool     `json:"favorite,omitempty"`
	FolderIDs     []string `json:"folderIds,omitempty"`
	Statuses      []string `json:"statuses,omitempty"`
	MediaTypes    []string `json:"mediaTypes,omitempty"`
	SourceKinds   []string `json:"sourceKinds,omitempty"`
	FavoritesOnly bool     `json:"favoritesOnly,omitempty"`
	TagMode       string   `json:"tagMode,omitempty"`
}

type SavedSearchSort struct {
	Field     string `json:"field,omitempty"`
	Direction string `json:"direction,omitempty"`
}

type VirtualFolder struct {
	ID        string    `json:"id"`
	LibraryID string    `json:"libraryId"`
	Name      string    `json:"name"`
	ParentID  string    `json:"parentId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SourceWatch struct {
	ID          string     `json:"id"`
	LibraryID   string     `json:"libraryId"`
	RootPath    string     `json:"rootPath"`
	Recursive   bool       `json:"recursive"`
	Enabled     bool       `json:"enabled"`
	LastScanAt  *time.Time `json:"lastScanAt,omitempty"`
	LastMessage string     `json:"lastMessage,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type TrashEntry struct {
	Kind        string    `json:"kind"`
	ID          string    `json:"id"`
	LibraryID   string    `json:"libraryId,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	DeletedAt   time.Time `json:"deletedAt"`
	ItemCount   int       `json:"itemCount,omitempty"`
}

type BackupRecord struct {
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}

type DocumentBatchRequest struct {
	DocumentIDs []string `json:"documentIds" binding:"required"`
	Action      string   `json:"action" binding:"required"`
	Tags        []string `json:"tags,omitempty"`
	FolderIDs   []string `json:"folderIds,omitempty"`
	Favorite    *bool    `json:"favorite,omitempty"`
}

type DocumentBatchResult struct {
	LibraryID    string   `json:"libraryId"`
	UpdatedCount int      `json:"updatedCount"`
	DocumentIDs  []string `json:"documentIds"`
	JobIDs       []string `json:"jobIds,omitempty"`
}

type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Code      string `json:"code"`
	RequestID string `json:"requestId,omitempty"`
	Retryable bool   `json:"retryable"`
}

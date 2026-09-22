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
	LibraryID       string `json:"libraryId"`
	Title           string `json:"title"`
	SourceType      string `json:"sourceType,omitempty"`
	ReviewStatus    string `json:"reviewStatus,omitempty"`
	ProjectID       string `json:"projectId,omitempty"`
	SourceURI       string `json:"sourceURI,omitempty"`
	FilePath        string `json:"filePath,omitempty"`
	LineStart       int    `json:"lineStart,omitempty"`
	LineEnd         int    `json:"lineEnd,omitempty"`
	PermissionScope string `json:"permissionScope,omitempty"`
	Language        string `json:"language,omitempty"`
	Symbol          string `json:"symbol,omitempty"`
	Scores          Scores `json:"scores"`
}

type QueryRequest struct {
	Query         string   `json:"query" binding:"required"`
	LibraryIDs    []string `json:"libraryIds"`
	ProjectIDs    []string `json:"projectIds"`
	SourceType    string   `json:"sourceType"`
	SourceTypes   []string `json:"sourceTypes"`
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
	RequestID         string     `json:"requestId"`
	Evidence          []Evidence `json:"evidence"`
	RequiredSkills    []Skill    `json:"requiredSkills,omitempty"`
	Answer            string     `json:"answer,omitempty"`
	Source            string     `json:"source,omitempty"`
	Generation        string     `json:"generation,omitempty"`
	SemanticAvailable bool       `json:"semanticAvailable"`
	KeywordOnly       bool       `json:"keywordOnly"`
	ModelID           string     `json:"modelId,omitempty"`
	ModelVersion      string     `json:"modelVersion,omitempty"`
	ConfigHash        string     `json:"configHash,omitempty"`
	Dimension         int        `json:"dimension,omitempty"`
	CorpusComplete    bool       `json:"corpusComplete"`
	NeedsRebuild      bool       `json:"needsRebuild"`
	RetrievalMode     string     `json:"retrievalMode"`
	Degraded          bool       `json:"degraded"`
	DegradedReason    string     `json:"degradedReason,omitempty"`
}

// RetrievalChunk is the cross-source citation contract used by the unified
// local retrieval index. Source-specific fields stay optional so legacy raw
// document chunks can be upgraded without rewriting their database rows.
type RetrievalChunk struct {
	ID              string         `json:"id"`
	DocumentID      string         `json:"documentId,omitempty"`
	Text            string         `json:"text"`
	SourceType      string         `json:"sourceType"`
	ReviewStatus    string         `json:"reviewStatus"`
	LibraryID       string         `json:"libraryId"`
	ProjectID       string         `json:"projectId,omitempty"`
	SourceURI       string         `json:"sourceURI,omitempty"`
	Title           string         `json:"title,omitempty"`
	FilePath        string         `json:"filePath,omitempty"`
	LineStart       int            `json:"lineStart,omitempty"`
	LineEnd         int            `json:"lineEnd,omitempty"`
	ContentHash     string         `json:"contentHash"`
	Symbol          string         `json:"symbol,omitempty"`
	Language        string         `json:"language,omitempty"`
	PermissionScope string         `json:"permissionScope,omitempty"`
	Location        map[string]any `json:"location,omitempty"`
	TokenCount      int            `json:"tokenCount,omitempty"`
	TokenLimit      int            `json:"tokenLimit,omitempty"`
	Truncated       bool           `json:"truncated,omitempty"`
}

type RetrievalScores struct {
	Lexical     float64 `json:"lexical"`
	Vector      float64 `json:"vector"`
	Fusion      float64 `json:"fusion"`
	Final       float64 `json:"final"`
	SymbolMatch bool    `json:"symbolMatch,omitempty"`
}

type RetrievalResult struct {
	RetrievalChunk
	Scores RetrievalScores `json:"scores"`
}

type RetrievalFilters struct {
	SourceType        string   `json:"sourceType,omitempty"`
	SourceTypes       []string `json:"sourceTypes,omitempty"`
	ReviewStatuses    []string `json:"reviewStatuses,omitempty"`
	LibraryIDs        []string `json:"libraryIds,omitempty"`
	AllowedLibraryIDs []string `json:"allowedLibraryIds,omitempty"`
	ProjectIDs        []string `json:"projectIds,omitempty"`
	AllowedProjectIDs []string `json:"allowedProjectIds,omitempty"`
	AllowRaw          bool     `json:"allowRaw"`
}

type RetrievalSearchRequest struct {
	Query         string           `json:"query" binding:"required"`
	Filters       RetrievalFilters `json:"filters"`
	TopK          int              `json:"topK,omitempty"`
	RetrievalMode string           `json:"retrievalMode,omitempty"`
}

type RetrievalSearchResponse struct {
	Results           []RetrievalResult `json:"results"`
	Source            string            `json:"source,omitempty"`
	Generation        string            `json:"generation,omitempty"`
	SemanticAvailable bool              `json:"semanticAvailable"`
	KeywordOnly       bool              `json:"keywordOnly"`
	Degraded          bool              `json:"degraded"`
	DegradedReason    string            `json:"degradedReason,omitempty"`
	ModelID           string            `json:"modelId,omitempty"`
	ModelVersion      string            `json:"modelVersion,omitempty"`
	ConfigHash        string            `json:"configHash,omitempty"`
	Dimension         int               `json:"dimension,omitempty"`
	CorpusComplete    bool              `json:"corpusComplete"`
	NeedsRebuild      bool              `json:"needsRebuild"`
	RetrievalMode     string            `json:"retrievalMode"`
}

// EvidenceReadResponse is the authoritative citation payload returned by the
// evidence MCP tool. It deliberately reuses the retrieval citation contract;
// source type, review state, locator and content hash therefore remain
// visible to callers instead of being hidden in a worker-only result.
type EvidenceReadResponse struct {
	Result RetrievalResult `json:"result"`
}

type RetrievalGeneration struct {
	LibraryID         string `json:"libraryId"`
	Generation        string `json:"generation"`
	Status            string `json:"status,omitempty"`
	Count             int    `json:"count,omitempty"`
	SemanticAvailable bool   `json:"semanticAvailable"`
	KeywordOnly       bool   `json:"keywordOnly,omitempty"`
	Degraded          bool   `json:"degraded,omitempty"`
	DegradedReason    string `json:"degradedReason,omitempty"`
	ModelID           string `json:"modelId,omitempty"`
	ModelVersion      string `json:"modelVersion,omitempty"`
	ConfigHash        string `json:"configHash,omitempty"`
	Dimension         int    `json:"dimension,omitempty"`
	CorpusComplete    bool   `json:"corpusComplete,omitempty"`
	NeedsRebuild      bool   `json:"needsRebuild,omitempty"`
}

type RetrievalProgress struct {
	LibraryID  string `json:"libraryId"`
	Generation string `json:"generation,omitempty"`
	Status     string `json:"status"`
	Completed  int    `json:"completed"`
	Total      int    `json:"total"`
	Error      string `json:"error,omitempty"`
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
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	LibraryIDs []string `json:"libraryIds"`
	// ProjectIDs is an explicit grant for project architecture evidence. It is
	// intentionally separate from LibraryIDs so an MCP token cannot infer
	// access to every source project in a library.
	ProjectIDs []string   `json:"projectIds"`
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

// Project is the durable scope for architecture scans and points at one source
// directory associated with exactly one knowledge library.
type Project struct {
	ID                string                 `json:"id"`
	LibraryID         string                 `json:"libraryId"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description,omitempty"`
	RootPath          string                 `json:"rootPath"`
	ExcludePatterns   []string               `json:"excludePatterns"`
	ScanPeriodSeconds int                    `json:"scanPeriodSeconds"`
	ScheduleKind      string                 `json:"scheduleKind"`
	ScheduleAnchor    string                 `json:"scheduleAnchor,omitempty"`
	ScheduleNextAt    *time.Time             `json:"scheduleNextAt,omitempty"`
	AllowRemoteCode   bool                   `json:"allowRemoteCode"`
	Execution         ProjectExecutionConfig `json:"execution"`
	LastSnapshotID    string                 `json:"lastSnapshotId,omitempty"`
	LastSnapshotHash  string                 `json:"lastSnapshotHash,omitempty"`
	LastScanChanged   *bool                  `json:"lastScanChanged,omitempty"`
	AnalysisStatus    string                 `json:"analysisStatus,omitempty"`
	AnalysisError     string                 `json:"analysisError,omitempty"`
	CurrentAnalysisID string                 `json:"currentAnalysisSnapshotId,omitempty"`
	CreatedAt         time.Time              `json:"createdAt"`
	UpdatedAt         time.Time              `json:"updatedAt"`
	DeletedAt         *time.Time             `json:"deletedAt,omitempty"`
}

// ProjectExecutionConfig is deliberately declarative.  It records what a
// later stage may use, but stage 1 never executes an Agent or remote command.
type ProjectExecutionConfig struct {
	ProviderID     string `json:"providerId,omitempty"`
	Executor       string `json:"executor"`
	CodexPath      string `json:"codexPath,omitempty"`
	AllowExternal  bool   `json:"allowExternal"`
	CodexModel     string `json:"codexModel,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type ArchitectureSnapshot struct {
	ID              string               `json:"id"`
	ProjectID       string               `json:"projectId"`
	Generation      int64                `json:"generation"`
	Status          string               `json:"status"`
	ContentHash     string               `json:"contentHash"`
	GitRevision     string               `json:"gitRevision,omitempty"`
	GitDirty        bool                 `json:"gitDirty"`
	GitChangedPaths []string             `json:"gitChangedPaths"`
	FileCount       int                  `json:"fileCount"`
	ModuleCount     int                  `json:"moduleCount"`
	UnknownCount    int                  `json:"unknownCount"`
	Coverage        ArchitectureCoverage `json:"coverage"`
	Files           []ArchitectureFile   `json:"files"`
	Modules         []ArchitectureModule `json:"modules"`
	CreatedAt       time.Time            `json:"createdAt"`
}

type ArchitectureFile struct {
	Path            string   `json:"path"`
	Language        string   `json:"language"`
	Size            int64    `json:"size"`
	LineStart       int      `json:"lineStart"`
	LineEnd         int      `json:"lineEnd"`
	LineCount       int      `json:"lineCount"`
	ContentHash     string   `json:"contentHash"`
	GitStatus       string   `json:"gitStatus,omitempty"`
	Ignored         bool     `json:"ignored,omitempty"`
	EntryPoint      bool     `json:"entryPoint"`
	DependencyFiles []string `json:"dependencyFiles,omitempty"`
	ContainerFiles  []string `json:"containerFiles,omitempty"`
	Detection       string   `json:"detection,omitempty"`
}

type ArchitectureModule struct {
	Name               string            `json:"name"`
	Language           string            `json:"language"`
	RootPath           string            `json:"rootPath"`
	Version            string            `json:"version,omitempty"`
	Dependencies       []string          `json:"dependencies"`
	DependencyVersions map[string]string `json:"dependencyVersions,omitempty"`
	EntryPoints        []string          `json:"entryPoints"`
	DependencyFiles    []string          `json:"dependencyFiles"`
	ContainerFiles     []string          `json:"containerFiles"`
	Unknown            bool              `json:"unknown"`
	EvidencePaths      []string          `json:"evidencePaths"`
}

type ArchitectureCoverage struct {
	TotalFiles     int            `json:"totalFiles"`
	IncludedFiles  int            `json:"includedFiles"`
	IgnoredFiles   int            `json:"ignoredFiles"`
	SecretFiles    int            `json:"secretFiles"`
	BinaryFiles    int            `json:"binaryFiles"`
	OutsideLinks   int            `json:"outsideLinks"`
	UnknownFiles   int            `json:"unknownFiles"`
	ByLanguage     map[string]int `json:"byLanguage"`
	IgnoredReasons map[string]int `json:"ignoredReasons"`
}

type ArchitectureTask struct {
	ID              string                       `json:"id"`
	ProjectID       string                       `json:"projectId"`
	Kind            string                       `json:"kind"`
	Input           *ArchitectureAnalysisRequest `json:"input,omitempty"`
	Status          string                       `json:"status"`
	Generation      int64                        `json:"generation"`
	LeaseOwner      string                       `json:"leaseOwner,omitempty"`
	LeaseToken      string                       `json:"leaseToken,omitempty"`
	LeaseExpiresAt  *time.Time                   `json:"leaseExpiresAt,omitempty"`
	SnapshotID      string                       `json:"snapshotId,omitempty"`
	CancelRequested bool                         `json:"cancelRequested"`
	ErrorCode       string                       `json:"errorCode,omitempty"`
	Message         string                       `json:"message,omitempty"`
	Executor        string                       `json:"executor,omitempty"`
	CreatedAt       time.Time                    `json:"createdAt"`
	UpdatedAt       time.Time                    `json:"updatedAt"`
}

// ArchitectureArtifact is an immutable, application-owned export generated
// from a snapshot. RelativePath is always under the application data root;
// the source project is never used as an output directory.
type ArchitectureArtifact struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	SnapshotID  string `json:"snapshotId"`
	Kind        string `json:"kind"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	// Renderer identifies the producer of the artifact.  A PNG produced by
	// the Go fallback is intentionally distinguishable from Archify output.
	Renderer      string    `json:"renderer,omitempty"`
	RendererError string    `json:"rendererError,omitempty"`
	RelativePath  string    `json:"relativePath"`
	ContentHash   string    `json:"contentHash"`
	Size          int64     `json:"size"`
	CreatedAt     time.Time `json:"createdAt"`
}

type ArchitectureDiff struct {
	ProjectID      string                `json:"projectId"`
	FromSnapshotID string                `json:"fromSnapshotId"`
	ToSnapshotID   string                `json:"toSnapshotId"`
	Added          []string              `json:"added"`
	Changed        []string              `json:"changed"`
	Deleted        []string              `json:"deleted"`
	Unchanged      []string              `json:"unchanged"`
	Analysis       *ArchitectureAnalysis `json:"analysis,omitempty"`
}

type ArchitectureAgent struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Capabilities  []string  `json:"capabilities"`
	ProjectIDs    []string  `json:"projectIds"`
	Status        string    `json:"status"`
	LastHeartbeat time.Time `json:"lastHeartbeat"`
	CreatedAt     time.Time `json:"createdAt"`
}

type ArchitectureAgentRegistration struct {
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	ProjectIDs   []string `json:"projectIds"`
}

// ArchitectureAgentCredential is returned only at registration time. The
// secret is stored as a hash and is never included in agent listings or task
// responses.
type ArchitectureAgentCredential struct {
	Agent  ArchitectureAgent `json:"agent"`
	Secret string            `json:"secret"`
}

type ArchitectureAgentLease struct {
	AgentID      string           `json:"agentId"`
	Task         ArchitectureTask `json:"task"`
	LeaseToken   string           `json:"leaseToken"`
	LeaseExpires time.Time        `json:"leaseExpires"`
}

type ArchitectureAnalysis struct {
	SchemaVersion string               `json:"schemaVersion"`
	ProjectID     string               `json:"projectId"`
	SnapshotID    string               `json:"snapshotId"`
	Facts         []AnalysisFact       `json:"facts"`
	Inferences    []AnalysisInference  `json:"inferences"`
	Evidence      []AnalysisEvidence   `json:"evidence"`
	Incremental   AnalysisIncremental  `json:"incremental"`
	Coverage      ArchitectureCoverage `json:"coverage"`
}

// ArchitectureAnalysisRequest is the stable input contract shared by local
// Codex, registered MCP agents and configured providers. Incremental paths
// are advisory for focused work; the complete snapshot remains the source of
// truth and must be validated before publication.
type ArchitectureAnalysisRequest struct {
	Project     Project              `json:"project"`
	Snapshot    ArchitectureSnapshot `json:"snapshot"`
	Incremental AnalysisIncremental  `json:"incremental"`
	Prompt      string               `json:"prompt,omitempty"`
}

type AnalysisFact struct {
	ID        string   `json:"id"`
	Subject   string   `json:"subject"`
	Predicate string   `json:"predicate"`
	Object    string   `json:"object"`
	Evidence  []string `json:"evidence"`
}

type AnalysisInference struct {
	ID         string   `json:"id"`
	Statement  string   `json:"statement"`
	Evidence   []string `json:"evidence"`
	Confidence float64  `json:"confidence"`
}

type AnalysisEvidence struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	LineStart   int    `json:"lineStart"`
	LineEnd     int    `json:"lineEnd"`
	ContentHash string `json:"contentHash"`
	Kind        string `json:"kind"`
}

type AnalysisIncremental struct {
	BaseSnapshotID string   `json:"baseSnapshotId,omitempty"`
	Added          []string `json:"added"`
	Changed        []string `json:"changed"`
	Deleted        []string `json:"deleted"`
	Unchanged      []string `json:"unchanged"`
}

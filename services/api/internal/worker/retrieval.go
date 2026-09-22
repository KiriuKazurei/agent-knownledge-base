package worker

import "context"

// RetrievalChunk is the shared source and citation contract sent to the
// offline worker. The API remains responsible for constructing this list from
// its authoritative SQLite records and for applying the caller's scope.
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

type RetrievalRebuildRequest struct {
	LibraryID string           `json:"libraryId"`
	Chunks    []RetrievalChunk `json:"chunks"`
	ModelID   string           `json:"modelId,omitempty"`
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
	Query         string           `json:"query"`
	Filters       RetrievalFilters `json:"filters"`
	TopK          int              `json:"topK,omitempty"`
	RetrievalMode string           `json:"retrievalMode,omitempty"`
}

type RetrievalResult struct {
	RetrievalChunk
	Lexical     float64 `json:"lexical"`
	Vector      float64 `json:"vector"`
	Fusion      float64 `json:"fusion"`
	Final       float64 `json:"final"`
	SymbolMatch bool    `json:"symbolMatch,omitempty"`
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

type RetrievalProgress struct {
	LibraryID  string `json:"libraryId"`
	Generation string `json:"generation,omitempty"`
	Status     string `json:"status"`
	Completed  int    `json:"completed"`
	Total      int    `json:"total"`
	Error      string `json:"error,omitempty"`
}

func (c *Client) RetrievalRebuild(ctx context.Context, request RetrievalRebuildRequest, out *RetrievalGeneration) error {
	return c.Call(ctx, "retrieval_rebuild", request, out)
}

func (c *Client) RetrievalSearch(ctx context.Context, request RetrievalSearchRequest, out *RetrievalSearchResponse) error {
	return c.Call(ctx, "retrieval_search", request, out)
}

func (c *Client) RetrievalUpsert(ctx context.Context, libraryID, documentID string, chunks []RetrievalChunk, out *RetrievalGeneration) error {
	return c.Call(ctx, "retrieval_upsert", map[string]any{"libraryId": libraryID, "documentId": documentID, "chunks": chunks}, out)
}

func (c *Client) RetrievalDelete(ctx context.Context, libraryID, documentID string, out *RetrievalGeneration) error {
	return c.Call(ctx, "retrieval_delete", map[string]any{"libraryId": libraryID, "documentId": documentID}, out)
}

func (c *Client) RetrievalCancel(ctx context.Context, libraryID string, out any) error {
	return c.Call(ctx, "retrieval_cancel", map[string]any{"libraryId": libraryID}, out)
}

func (c *Client) RetrievalProgress(ctx context.Context, libraryID string, out *RetrievalProgress) error {
	return c.Call(ctx, "retrieval_progress", map[string]any{"libraryId": libraryID}, out)
}

func (c *Client) RetrievalStatus(ctx context.Context, libraryID string, out *RetrievalGeneration) error {
	return c.Call(ctx, "retrieval_status", map[string]any{"libraryId": libraryID}, out)
}

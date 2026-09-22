package worker

import "context"

// Embedding model calls stay small and typed at the Go/Worker boundary. The
// worker remains the authority for package validation and runtime health; the
// API only transports the verified result to the desktop client.
type EmbeddingModelHealth struct {
	Available      bool   `json:"available"`
	ModelID        string `json:"modelId"`
	Version        string `json:"version,omitempty"`
	Dimension      int    `json:"dimension,omitempty"`
	Runtime        string `json:"runtime,omitempty"`
	RuntimeVersion string `json:"runtimeVersion,omitempty"`
	RuntimeBytes   int64  `json:"runtimeBytes,omitempty"`
	Error          string `json:"error,omitempty"`
}

type EmbeddingModelStatus struct {
	ID           string                `json:"id"`
	Version      string                `json:"version,omitempty"`
	Installed    bool                  `json:"installed"`
	Enabled      bool                  `json:"enabled"`
	Loaded       bool                  `json:"loaded,omitempty"`
	SizeBytes    int64                 `json:"sizeBytes"`
	RuntimeBytes int64                 `json:"runtimeBytes"`
	Health       *EmbeddingModelHealth `json:"health,omitempty"`
}

func (c *Client) ModelLoad(ctx context.Context, modelID string, out *EmbeddingModelStatus) error {
	return c.Call(ctx, "model_load", map[string]any{"modelId": modelID}, out)
}

func (c *Client) ModelUnload(ctx context.Context, modelID string, out *EmbeddingModelStatus) error {
	return c.Call(ctx, "model_unload", map[string]any{"modelId": modelID}, out)
}

func (c *Client) ModelHealth(ctx context.Context, modelID string, out *EmbeddingModelHealth) error {
	return c.Call(ctx, "model_health", map[string]any{"modelId": modelID}, out)
}

func (c *Client) ModelEmbed(ctx context.Context, modelID string, texts []string, query bool, out any) error {
	return c.Call(ctx, "model_embed", map[string]any{
		"modelId": modelID,
		"texts":   texts,
		"isQuery": query,
	}, out)
}

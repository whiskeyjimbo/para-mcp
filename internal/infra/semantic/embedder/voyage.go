package embedder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
)

var _ ports.Embedder = (*VoyageEmbedder)(nil)

const (
	voyageDefaultModel   = "voyage-3"
	voyageDefaultDims    = 1024
	voyageDefaultBaseURL = "https://api.voyageai.com/v1"
)

// VoyageConfig configures the Voyage AI embedder.
type VoyageConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	Dims       int
	MaxRetries int
}

// VoyageEmbedder implements the Embedder port via the Voyage AI REST API.
type VoyageEmbedder struct {
	cfg    VoyageConfig
	client *http.Client
}

// NewVoyage creates a VoyageEmbedder.
func NewVoyage(cfg VoyageConfig) *VoyageEmbedder {
	if cfg.BaseURL == "" {
		cfg.BaseURL = voyageDefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = voyageDefaultModel
	}
	if cfg.Dims == 0 {
		cfg.Dims = voyageDefaultDims
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	return &VoyageEmbedder{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (e *VoyageEmbedder) ModelName() string { return e.cfg.Model }
func (e *VoyageEmbedder) Dims() int         { return e.cfg.Dims }

type voyageRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type voyageResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed converts texts to vectors, retrying on 429 and 5xx errors.
func (e *VoyageEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	payload, err := json.Marshal(voyageRequest{Input: texts, Model: e.cfg.Model})
	if err != nil {
		return nil, err
	}
	body, err := doWithRetry(ctx, e.client, http.MethodPost, e.cfg.BaseURL+"/embeddings",
		"Bearer "+e.cfg.APIKey, payload, e.cfg.MaxRetries)
	if err != nil {
		return nil, err
	}
	var r voyageResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse voyage response: %w", err)
	}
	out := make([][]float32, len(r.Data))
	for i, d := range r.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

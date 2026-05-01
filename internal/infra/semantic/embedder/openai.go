package embedder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/ports"
)

var _ ports.Embedder = (*OpenAIEmbedder)(nil)

const (
	openaiDefaultModel   = "text-embedding-3-small"
	openaiDefaultDims    = 1536
	openaiDefaultBaseURL = "https://api.openai.com/v1"
)

// OpenAIConfig configures the OpenAI embedder.
type OpenAIConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	Dims       int
	MaxRetries int
}

// OpenAIEmbedder implements the Embedder port via the OpenAI embeddings API.
type OpenAIEmbedder struct {
	cfg    OpenAIConfig
	client *http.Client
}

// NewOpenAI creates an OpenAIEmbedder.
func NewOpenAI(cfg OpenAIConfig) *OpenAIEmbedder {
	if cfg.BaseURL == "" {
		cfg.BaseURL = openaiDefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = openaiDefaultModel
	}
	if cfg.Dims == 0 {
		cfg.Dims = openaiDefaultDims
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	return &OpenAIEmbedder{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (e *OpenAIEmbedder) ModelName() string { return e.cfg.Model }
func (e *OpenAIEmbedder) Dims() int         { return e.cfg.Dims }

type openaiRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type openaiResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed converts texts to vectors, retrying on 429 and 5xx errors.
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	payload, err := json.Marshal(openaiRequest{Input: texts, Model: e.cfg.Model})
	if err != nil {
		return nil, err
	}
	body, err := doWithRetry(ctx, e.client, http.MethodPost, e.cfg.BaseURL+"/embeddings",
		"Bearer "+e.cfg.APIKey, payload, e.cfg.MaxRetries)
	if err != nil {
		return nil, err
	}
	var r openaiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse openai response: %w", err)
	}
	out := make([][]float32, len(r.Data))
	for _, d := range r.Data {
		if d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	return out, nil
}

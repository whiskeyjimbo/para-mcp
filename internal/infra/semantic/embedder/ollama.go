// Package embedder provides Embedder port implementations for Ollama, Voyage, and OpenAI.
package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
)

var _ ports.Embedder = (*OllamaEmbedder)(nil)

const (
	ollamaDefaultModel = "nomic-embed-text"
	ollamaDefaultDims  = 768
	ollamaDefaultBase  = "http://localhost:11434"
	ollamaUnloadAfter  = 10 * time.Minute
	maxEmbedBody       = 4 << 20 // 4 MiB
)

// OllamaConfig configures the Ollama embedder.
type OllamaConfig struct {
	BaseURL     string
	Model       string
	Dims        int
	UnloadAfter time.Duration // default 10 min
}

// OllamaEmbedder calls the local Ollama HTTP API to embed text.
// It prewarns the model on user-intent signals and unloads it after inactivity.
type OllamaEmbedder struct {
	cfg    OllamaConfig
	client *http.Client

	mu    sync.Mutex
	timer *time.Timer
}

// NewOllama creates an OllamaEmbedder with the given config.
func NewOllama(cfg OllamaConfig) *OllamaEmbedder {
	if cfg.BaseURL == "" {
		cfg.BaseURL = ollamaDefaultBase
	}
	if cfg.Model == "" {
		cfg.Model = ollamaDefaultModel
	}
	if cfg.Dims == 0 {
		cfg.Dims = ollamaDefaultDims
	}
	if cfg.UnloadAfter == 0 {
		cfg.UnloadAfter = ollamaUnloadAfter
	}
	return &OllamaEmbedder{
		cfg:    cfg,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (e *OllamaEmbedder) ModelName() string { return e.cfg.Model }
func (e *OllamaEmbedder) Dims() int         { return e.cfg.Dims }

// Embed converts texts to vectors. Returns a descriptive error if Ollama is unreachable.
func (e *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vec, err := e.embedOne(ctx, text)
		if err != nil {
			return nil, err
		}
		out = append(out, vec)
	}
	e.resetUnloadTimer()
	return out, nil
}

// Prewarm sends a keep-alive request to load the model; is a no-op if Ollama is unreachable.
func (e *OllamaEmbedder) Prewarm(ctx context.Context) {
	payload, _ := json.Marshal(map[string]any{"model": e.cfg.Model, "keep_alive": "10m"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.BaseURL+"/api/generate", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return // graceful degradation
	}
	resp.Body.Close()
}

type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

func (e *OllamaEmbedder) embedOne(ctx context.Context, text string) ([]float32, error) {
	payload, err := json.Marshal(ollamaEmbedRequest{Model: e.cfg.Model, Prompt: text})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.BaseURL+"/api/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama unreachable: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbedBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama status %d: %s", resp.StatusCode, body)
	}
	var r ollamaEmbedResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse ollama response: %w", err)
	}
	if len(r.Embedding) == 0 {
		return nil, errors.New("ollama returned empty embedding")
	}
	return r.Embedding, nil
}

// resetUnloadTimer arms (or re-arms) the inactivity unload timer after each Embed call.
// Uses Stop+new instead of Reset to avoid AfterFunc reset race documented in time package.
func (e *OllamaEmbedder) resetUnloadTimer() {
	if e.cfg.UnloadAfter <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.timer != nil {
		e.timer.Stop()
	}
	e.timer = time.AfterFunc(e.cfg.UnloadAfter, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.unload(ctx)
	})
}

// Close stops the unload timer. Should be called when the embedder is no longer needed.
func (e *OllamaEmbedder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
}

func (e *OllamaEmbedder) unload(ctx context.Context) {
	payload, _ := json.Marshal(map[string]any{"model": e.cfg.Model, "keep_alive": "0"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.BaseURL+"/api/generate", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

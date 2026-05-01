package embedder_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/whiskeyjimbo/paras/internal/infra/semantic/embedder"
)

// --- Voyage tests ---

func voyageResp(embeddings [][]float32) []byte {
	type item struct {
		Embedding []float32 `json:"embedding"`
	}
	var data []item
	for _, e := range embeddings {
		data = append(data, item{Embedding: e})
	}
	b, _ := json.Marshal(map[string]any{"data": data, "usage": map[string]any{"total_tokens": 10}})
	return b
}

func TestVoyageEmbedReturnsVectors(t *testing.T) {
	want := [][]float32{{0.1, 0.2}, {0.3, 0.4}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(voyageResp(want))
	}))
	defer srv.Close()

	e := embedder.NewVoyage(embedder.VoyageConfig{APIKey: "test", BaseURL: srv.URL})
	got, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(got))
	}
}

func TestVoyageRetryOn429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write(voyageResp([][]float32{{0.1}}))
	}))
	defer srv.Close()

	e := embedder.NewVoyage(embedder.VoyageConfig{APIKey: "test", BaseURL: srv.URL, MaxRetries: 3})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if calls < 3 {
		t.Errorf("expected at least 3 calls, got %d", calls)
	}
}

func TestVoyageNonRetryableErrorImmediate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	e := embedder.NewVoyage(embedder.VoyageConfig{APIKey: "bad", BaseURL: srv.URL, MaxRetries: 3})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

func TestVoyageDimsAndModel(t *testing.T) {
	e := embedder.NewVoyage(embedder.VoyageConfig{APIKey: "test"})
	if e.ModelName() == "" {
		t.Error("ModelName should not be empty")
	}
	if e.Dims() <= 0 {
		t.Errorf("Dims should be positive, got %d", e.Dims())
	}
}

// --- OpenAI tests ---

func openaiResp(embeddings [][]float32) []byte {
	type item struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	}
	var data []item
	for i, e := range embeddings {
		data = append(data, item{Embedding: e, Index: i})
	}
	b, _ := json.Marshal(map[string]any{
		"data":  data,
		"usage": map[string]any{"prompt_tokens": 5, "total_tokens": 5},
	})
	return b
}

func TestOpenAIEmbedReturnsVectors(t *testing.T) {
	want := [][]float32{{0.5, 0.6}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(openaiResp(want))
	}))
	defer srv.Close()

	e := embedder.NewOpenAI(embedder.OpenAIConfig{APIKey: "test", BaseURL: srv.URL})
	got, err := e.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(got))
	}
}

func TestOpenAIRetryOn500(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(openaiResp([][]float32{{0.1}}))
	}))
	defer srv.Close()

	e := embedder.NewOpenAI(embedder.OpenAIConfig{APIKey: "test", BaseURL: srv.URL, MaxRetries: 3})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatalf("expected retry success: %v", err)
	}
}

func TestOpenAIDimsAndModel(t *testing.T) {
	e := embedder.NewOpenAI(embedder.OpenAIConfig{APIKey: "test"})
	if e.ModelName() == "" {
		t.Error("ModelName should not be empty")
	}
	if e.Dims() <= 0 {
		t.Errorf("Dims should be positive, got %d", e.Dims())
	}
}

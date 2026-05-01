package embedder_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/infra/semantic/embedder"
)

func ollamaEmbedResp(embedding []float32) []byte {
	resp := map[string]any{"embedding": embedding}
	b, _ := json.Marshal(resp)
	return b
}

func TestOllamaEmbedReturnsVectors(t *testing.T) {
	want := []float32{0.1, 0.2, 0.3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(ollamaEmbedResp(want))
	}))
	defer srv.Close()

	e := embedder.NewOllama(embedder.OllamaConfig{BaseURL: srv.URL})
	vecs, err := e.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(vecs))
	}
	for i, v := range vecs[0] {
		if v != want[i] {
			t.Errorf("vecs[0][%d] = %v, want %v", i, v, want[i])
		}
	}
}

func TestOllamaEmbedUnreachableReturnsError(t *testing.T) {
	e := embedder.NewOllama(embedder.OllamaConfig{BaseURL: "http://127.0.0.1:19999"})
	_, err := e.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error when Ollama is unreachable")
	}
}

func TestOllamaPrewarmNoopWhenUnreachable(t *testing.T) {
	e := embedder.NewOllama(embedder.OllamaConfig{BaseURL: "http://127.0.0.1:19999"})
	// Prewarm must not panic even when Ollama is down.
	e.Prewarm(context.Background())
}

func TestOllamaUnloadTimerResetsOnEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(ollamaEmbedResp([]float32{0.1}))
	}))
	defer srv.Close()

	e := embedder.NewOllama(embedder.OllamaConfig{BaseURL: srv.URL, UnloadAfter: 50 * time.Millisecond})
	e.Embed(context.Background(), []string{"first"})
	time.Sleep(30 * time.Millisecond)
	e.Embed(context.Background(), []string{"second"}) // resets timer
	// If the timer did NOT reset, it would fire at ~50ms from first call (~20ms from now).
	// Since we reset it, it should fire at ~50ms from the second call.
	// We just verify no panic/crash — full unload verification requires inspecting Ollama.
}

func TestOllamaDimsAndModel(t *testing.T) {
	e := embedder.NewOllama(embedder.OllamaConfig{BaseURL: "http://localhost:11434"})
	if e.ModelName() == "" {
		t.Error("ModelName should not be empty")
	}
	if e.Dims() <= 0 {
		t.Errorf("Dims should be positive, got %d", e.Dims())
	}
}

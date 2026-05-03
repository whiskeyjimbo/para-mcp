package semantic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
	"github.com/whiskeyjimbo/para-mcp/internal/infra/semantic"
)

type fakeEmbedder struct {
	vec []float32
	err error
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = f.vec
	}
	return out, nil
}
func (f *fakeEmbedder) Dims() int         { return len(f.vec) }
func (f *fakeEmbedder) ModelName() string { return "fake" }

type fakeVS struct {
	hits    []domain.VectorHit
	err     error
	gotK    int
	gotVec  []float32
	gotAuth domain.AuthFilter
}

func (f *fakeVS) Upsert(context.Context, []domain.VectorRecord) error   { return nil }
func (f *fakeVS) Delete(context.Context, []string) error                { return nil }
func (f *fakeVS) Tombstone(context.Context, []string) error             { return nil }
func (f *fakeVS) ListTombstoned(context.Context, int) ([]string, error) { return nil, nil }
func (f *fakeVS) Close() error                                          { return nil }
func (f *fakeVS) Search(_ context.Context, vec []float32, filter domain.AuthFilter, k int) ([]domain.VectorHit, error) {
	f.gotK = k
	f.gotVec = vec
	f.gotAuth = filter
	return f.hits, f.err
}

var _ ports.VectorStore = (*fakeVS)(nil)
var _ ports.Embedder = (*fakeEmbedder)(nil)

func TestSearcher_OverFetchAndTrim(t *testing.T) {
	ref := func(p string) domain.NoteRef { return domain.NoteRef{Scope: "personal", Path: p} }
	vs := &fakeVS{hits: []domain.VectorHit{
		{Ref: ref("a.md"), Chunk: 0, Score: 0.9},
		{Ref: ref("b.md"), Chunk: 0, Score: 0.8},
		{Ref: ref("c.md"), Chunk: 0, Score: 0.7},
		{Ref: ref("d.md"), Chunk: 0, Score: 0.6},
	}}
	emb := &fakeEmbedder{vec: []float32{0.1, 0.2}}
	s := semantic.NewSearcher(emb, vs, 4)
	got, err := s.SemanticSearch(context.Background(), "q",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.SemanticSearchOptions{Limit: 2})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if vs.gotK != 8 {
		t.Errorf("over-fetch k: got %d, want 2*4=8", vs.gotK)
	}
	if len(got) != 2 {
		t.Fatalf("trim to limit: got %d, want 2", len(got))
	}
	if got[0].Ref.Path != "a.md" || got[1].Ref.Path != "b.md" {
		t.Errorf("not sorted by score desc: %+v", got)
	}
}

func TestSearcher_ThresholdFloor(t *testing.T) {
	ref := func(p string) domain.NoteRef { return domain.NoteRef{Scope: "personal", Path: p} }
	vs := &fakeVS{hits: []domain.VectorHit{
		{Ref: ref("a.md"), Score: 0.9},
		{Ref: ref("b.md"), Score: 0.4},
	}}
	s := semantic.NewSearcher(&fakeEmbedder{vec: []float32{0.1}}, vs, 4)
	got, err := s.SemanticSearch(context.Background(), "q",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.SemanticSearchOptions{Limit: 10, Threshold: 0.5})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("threshold should drop 0.4: got %d", len(got))
	}
	if got[0].Ref.Path != "a.md" {
		t.Errorf("expected a.md, got %s", got[0].Ref.Path)
	}
}

func TestSearcher_PropagatesEmbedderError(t *testing.T) {
	emb := &fakeEmbedder{err: errors.New("boom")}
	s := semantic.NewSearcher(emb, &fakeVS{}, 4)
	_, err := s.SemanticSearch(context.Background(), "q",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.SemanticSearchOptions{Limit: 5})
	if err == nil {
		t.Fatal("expected error from embedder")
	}
}

func TestSearcher_DedupesChunksToOneRef(t *testing.T) {
	ref := domain.NoteRef{Scope: "personal", Path: "a.md"}
	vs := &fakeVS{hits: []domain.VectorHit{
		{Ref: ref, Chunk: 0, Score: 0.9},
		{Ref: ref, Chunk: 1, Score: 0.7},
		{Ref: ref, Chunk: 2, Score: 0.65},
	}}
	s := semantic.NewSearcher(&fakeEmbedder{vec: []float32{0.1}}, vs, 4)
	got, err := s.SemanticSearch(context.Background(), "q",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.SemanticSearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("chunks should dedupe to 1 per ref, got %d", len(got))
	}
}

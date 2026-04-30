package index

import (
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

func ref(scope, path string) domain.NoteRef {
	return domain.NoteRef{Scope: scope, Path: path}
}

func addAndFlush(t *testing.T, idx *Index, docs ...Doc) {
	t.Helper()
	for _, d := range docs {
		idx.Add(d)
	}
	done := make(chan struct{}, 1)
	idx.ch <- writeOp{syncDone: done}
	<-done
}

func flush(t *testing.T, idx *Index) {
	t.Helper()
	done := make(chan struct{}, 1)
	idx.ch <- writeOp{syncDone: done}
	<-done
}

func TestIndex_BasicSearch(t *testing.T) {
	idx := New(Config{})
	defer idx.Close()

	addAndFlush(t, idx, Doc{
		Ref:   ref("personal", "projects/aws.md"),
		Title: "AWS Networking",
		Body:  "This note covers AWS VPC configuration and routing.",
	})

	results := idx.Search("aws networking", 10)
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Ref != ref("personal", "projects/aws.md") {
		t.Fatalf("unexpected top result: %v", results[0].Ref)
	}
}

func TestIndex_PorterStemming(t *testing.T) {
	idx := New(Config{Stemmer: StemmerPorter})
	defer idx.Close()

	addAndFlush(t, idx, Doc{
		Ref:  ref("personal", "projects/billing.md"),
		Body: "We need to handle invoicing for enterprise customers.",
	})

	results := idx.Search("invoice", 10)
	if len(results) == 0 {
		t.Fatal("Porter stemmer: 'invoice' should match 'invoicing'")
	}
}

func TestIndex_NoStemming(t *testing.T) {
	idx := New(Config{Stemmer: StemmerNone})
	defer idx.Close()

	addAndFlush(t, idx, Doc{
		Ref:  ref("personal", "projects/billing.md"),
		Body: "We need to handle invoicing for enterprise customers.",
	})

	results := idx.Search("invoice", 10)
	if len(results) != 0 {
		t.Fatal("StemmerNone: 'invoice' should not match 'invoicing'")
	}
}

func TestIndex_TitleBoost(t *testing.T) {
	idx := New(Config{TitleBoost: 2.0})
	defer idx.Close()

	addAndFlush(t, idx,
		Doc{
			Ref:   ref("personal", "projects/title-match.md"),
			Title: "multitenant egress",
			Body:  "Some unrelated content here about nothing special.",
		},
		Doc{
			Ref:   ref("personal", "projects/body-match.md"),
			Title: "Unrelated Title",
			Body:  "This body mentions multitenant egress patterns extensively.",
		},
	)

	results := idx.Search("multitenant egress", 10)
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Ref.Path != "projects/title-match.md" {
		t.Fatalf("title match should rank first; got %v", results[0].Ref)
	}
}

func TestIndex_Remove(t *testing.T) {
	idx := New(Config{})
	defer idx.Close()

	r := ref("personal", "projects/temp.md")
	addAndFlush(t, idx, Doc{Ref: r, Body: "searchable content here"})

	if results := idx.Search("searchable", 10); len(results) == 0 {
		t.Fatal("doc should be findable before removal")
	}

	idx.Remove(r)
	flush(t, idx)

	if results := idx.Search("searchable", 10); len(results) != 0 {
		t.Fatal("doc should not appear after removal")
	}
}

func TestIndex_Update(t *testing.T) {
	idx := New(Config{})
	defer idx.Close()

	r := ref("personal", "projects/evolving.md")
	addAndFlush(t, idx, Doc{Ref: r, Body: "original content about golang"})

	if results := idx.Search("golang", 10); len(results) == 0 {
		t.Fatal("should find original content")
	}

	addAndFlush(t, idx, Doc{Ref: r, Body: "completely different topic about rust"})

	if results := idx.Search("golang", 10); len(results) != 0 {
		t.Fatal("old terms should be removed after update")
	}
	if results := idx.Search("rust", 10); len(results) == 0 {
		t.Fatal("new terms should be findable after update")
	}
}

func TestIndex_StopWordsNotIndexed(t *testing.T) {
	idx := New(Config{})
	defer idx.Close()

	addAndFlush(t, idx, Doc{
		Ref:  ref("personal", "projects/x.md"),
		Body: "the quick brown fox",
	})

	if results := idx.Search("the", 10); len(results) != 0 {
		t.Fatal("stop word 'the' should not match")
	}
}

func TestIndex_Ranking(t *testing.T) {
	idx := New(Config{})
	defer idx.Close()

	addAndFlush(t, idx,
		Doc{Ref: ref("personal", "projects/a.md"), Body: "kubernetes kubernetes kubernetes deployment"},
		Doc{Ref: ref("personal", "projects/b.md"), Body: "kubernetes deployment overview"},
	)

	results := idx.Search("kubernetes", 10)
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Ref.Path != "projects/a.md" {
		t.Fatalf("higher-TF doc should rank first; got %v", results[0].Ref)
	}
}

func TestIndex_UpdatedAt(t *testing.T) {
	idx := New(Config{})
	defer idx.Close()

	ts := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	addAndFlush(t, idx, Doc{
		Ref:       ref("personal", "projects/dated.md"),
		Body:      "temporal content",
		UpdatedAt: ts,
	})

	results := idx.Search("temporal", 10)
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if !results[0].UpdatedAt.Equal(ts) {
		t.Fatalf("UpdatedAt not preserved: got %v", results[0].UpdatedAt)
	}
}

func TestIndex_EmptySearch(t *testing.T) {
	idx := New(Config{})
	defer idx.Close()
	if results := idx.Search("", 10); results != nil {
		t.Fatal("empty query should return nil")
	}
}

func TestIndex_ConcurrentReads(t *testing.T) {
	idx := New(Config{})
	defer idx.Close()

	addAndFlush(t, idx, Doc{Ref: ref("personal", "projects/x.md"), Body: "concurrent access test"})

	done := make(chan struct{})
	for range 10 {
		go func() {
			idx.Search("concurrent", 10)
			done <- struct{}{}
		}()
	}
	for range 10 {
		<-done
	}
}

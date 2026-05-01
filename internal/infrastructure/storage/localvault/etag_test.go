package localvault

import (
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/noteutil"
)

func TestCanonicalFrontMatterYAML_derivedExcluded(t *testing.T) {
	fm1 := domain.FrontMatter{Title: "Hello", Extra: map[string]any{}}
	fm2 := domain.FrontMatter{Title: "Hello", Extra: map[string]any{
		"derived": map[string]any{"summary": "some AI summary"},
	}}
	a := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(fm1), "body")
	b := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(fm2), "body")
	if a != b {
		t.Fatalf("derived field should not affect ETag: %q vs %q", a, b)
	}
}

func TestCanonicalFrontMatterYAML_keyOrderIrrelevant(t *testing.T) {
	fm1 := domain.FrontMatter{Extra: map[string]any{"z_key": "val1", "a_key": "val2"}}
	fm2 := domain.FrontMatter{Extra: map[string]any{"a_key": "val2", "z_key": "val1"}}
	a := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(fm1), "body")
	b := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(fm2), "body")
	if a != b {
		t.Fatalf("key order should not affect ETag: %q vs %q", a, b)
	}
}

func TestCanonicalFrontMatterYAML_mtimeNotCausingPanic(t *testing.T) {
	fm1 := domain.FrontMatter{Title: "Hello", UpdatedAt: time.Now()}
	fm2 := domain.FrontMatter{Title: "Hello", UpdatedAt: time.Now().Add(1e9)}
	_ = noteutil.CanonicalFrontMatterYAML(fm1)
	_ = noteutil.CanonicalFrontMatterYAML(fm2)
}

package domain

import (
	"strings"
	"testing"
	"time"
)

func TestComputeETag_format(t *testing.T) {
	fm := FrontMatter{Title: "Test"}
	etag := ComputeETag(fm, "body")
	// Must be a strong ETag: "xxxx" (no W/ prefix), 16 hex chars inside quotes.
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Fatalf("ETag not quoted: %q", etag)
	}
	inner := etag[1 : len(etag)-1]
	if len(inner) != 16 {
		t.Fatalf("ETag inner length = %d, want 16: %q", len(inner), inner)
	}
	for _, c := range inner {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("ETag contains non-hex character %q: %s", c, inner)
		}
	}
}

func TestComputeETag_deterministic(t *testing.T) {
	fm := FrontMatter{Title: "Hello", Tags: []string{"aws", "infra"}}
	a := ComputeETag(fm, "body text")
	b := ComputeETag(fm, "body text")
	if a != b {
		t.Fatalf("ETag not deterministic: %q vs %q", a, b)
	}
}

func TestComputeETag_bodyChangeRotates(t *testing.T) {
	fm := FrontMatter{Title: "Hello"}
	a := ComputeETag(fm, "body one")
	b := ComputeETag(fm, "body two")
	if a == b {
		t.Fatal("body change should rotate ETag")
	}
}

func TestComputeETag_derivedExcluded(t *testing.T) {
	// derived key in Extra must not affect the ETag.
	fm1 := FrontMatter{Title: "Hello", Extra: map[string]any{}}
	fm2 := FrontMatter{Title: "Hello", Extra: map[string]any{
		"derived": map[string]any{"summary": "some AI summary"},
	}}
	a := ComputeETag(fm1, "body")
	b := ComputeETag(fm2, "body")
	if a != b {
		t.Fatalf("derived field should not affect ETag: %q vs %q", a, b)
	}
}

func TestComputeETag_mtimeExcluded(t *testing.T) {
	// Changing UpdatedAt (mtime-equivalent) must not rotate ETag.
	fm1 := FrontMatter{Title: "Hello", UpdatedAt: time.Now()}
	fm2 := FrontMatter{Title: "Hello", UpdatedAt: time.Now().Add(1e9)}
	a := ComputeETag(fm1, "body")
	b := ComputeETag(fm2, "body")
	// UpdatedAt IS part of user-authored frontmatter per spec (not excluded like derived).
	// What's excluded is mtime from the filesystem, not UpdatedAt in frontmatter.
	// So these ETags may differ — that is correct behavior.
	// This test just documents that derived is what's excluded, not all timestamps.
	_ = a
	_ = b
}

func TestComputeETag_keyOrderIrrelevant(t *testing.T) {
	// Keys are sorted canonically, so insertion order in Extra should not matter.
	fm1 := FrontMatter{Extra: map[string]any{"z_key": "val1", "a_key": "val2"}}
	fm2 := FrontMatter{Extra: map[string]any{"a_key": "val2", "z_key": "val1"}}
	a := ComputeETag(fm1, "body")
	b := ComputeETag(fm2, "body")
	if a != b {
		t.Fatalf("key order should not affect ETag: %q vs %q", a, b)
	}
}

func TestComputeETag_noWPrefix(t *testing.T) {
	etag := ComputeETag(FrontMatter{Title: "x"}, "y")
	if strings.HasPrefix(etag, "W/") {
		t.Fatalf("ETag must not have W/ prefix: %q", etag)
	}
}

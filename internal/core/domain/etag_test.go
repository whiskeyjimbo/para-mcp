package domain

import (
	"strings"
	"testing"
)

func TestComputeETag_format(t *testing.T) {
	etag := ComputeETag("title: Test", "body")
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
	a := ComputeETag("title: Hello\ntags:\n  - aws\n  - infra", "body text")
	b := ComputeETag("title: Hello\ntags:\n  - aws\n  - infra", "body text")
	if a != b {
		t.Fatalf("ETag not deterministic: %q vs %q", a, b)
	}
}

func TestComputeETag_bodyChangeRotates(t *testing.T) {
	a := ComputeETag("title: Hello", "body one")
	b := ComputeETag("title: Hello", "body two")
	if a == b {
		t.Fatal("body change should rotate ETag")
	}
}

func TestComputeETag_noWPrefix(t *testing.T) {
	etag := ComputeETag("title: x", "y")
	if strings.HasPrefix(etag, "W/") {
		t.Fatalf("ETag must not have W/ prefix: %q", etag)
	}
}

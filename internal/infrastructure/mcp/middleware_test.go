package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
)

// countingScopeResolver counts how many times Scopes is invoked.
type countingScopeResolver struct {
	calls atomic.Int64
	ids   []domain.ScopeID
}

func (c *countingScopeResolver) Scopes(_ context.Context) []domain.ScopeID {
	c.calls.Add(1)
	return c.ids
}

func TestMemoScopeResolver_SingleCallWithMemoSlot(t *testing.T) {
	inner := &countingScopeResolver{ids: []domain.ScopeID{"personal"}}
	memo := ports.NewMemoScopeResolver(inner)

	ctx := ports.WithScopeMemo(context.Background())
	_ = memo.Scopes(ctx)
	_ = memo.Scopes(ctx)
	_ = memo.Scopes(ctx)

	if got := inner.calls.Load(); got != 1 {
		t.Errorf("inner Scopes called %d times, want 1", got)
	}
}

func TestMemoScopeResolver_FallsThrough_WithoutMemoSlot(t *testing.T) {
	inner := &countingScopeResolver{ids: []domain.ScopeID{"personal"}}
	memo := ports.NewMemoScopeResolver(inner)

	ctx := context.Background()
	_ = memo.Scopes(ctx)
	_ = memo.Scopes(ctx)

	if got := inner.calls.Load(); got != 2 {
		t.Errorf("inner Scopes called %d times without memo slot, want 2", got)
	}
}

func TestScopeMemoMiddleware_InstallsMemoSlot(t *testing.T) {
	inner := &countingScopeResolver{ids: []domain.ScopeID{"personal"}}
	memo := ports.NewMemoScopeResolver(inner)

	var callsInHandler int64
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_ = memo.Scopes(r.Context())
		_ = memo.Scopes(r.Context())
		callsInHandler = inner.calls.Load()
	})

	h := ScopeMemoMiddleware(next)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if callsInHandler != 1 {
		t.Errorf("inner Scopes called %d times inside handler, want 1", callsInHandler)
	}
}

func TestRequestIDMiddleware_ValidHeader_Passes(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := RequestIDMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(requestIDHeader, "req_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !called {
		t.Error("inner handler should be called for a valid request ID")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestRequestIDMiddleware_MalformedHeader_InvalidArgument(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler must not be called for a malformed request ID")
	})
	h := RequestIDMiddleware(inner)

	cases := []string{
		"not-a-request-id",
		"req_",
		"req_toolong123456789012345678",
		"REQ_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"req_01ARZ3NDEKTSV4RRFFQ69G5FA!", // invalid character
	}
	for _, id := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set(requestIDHeader, id)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("id=%q: expected 400, got %d", id, rr.Code)
		}
		body := rr.Body.String()
		if len(body) < len("invalid_argument") {
			t.Errorf("id=%q: response body missing invalid_argument prefix: %q", id, body)
		}
	}
}

func TestRequestIDMiddleware_NoHeader_Passes(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := RequestIDMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !called {
		t.Error("inner handler should be called when header is absent")
	}
}

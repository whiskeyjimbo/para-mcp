package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/whiskeyjimbo/para-mcp/internal/server/auth"
)

func TestBearerMiddleware_NoToken(t *testing.T) {
	h := auth.BearerMiddleware(auth.WithTokenStore(auth.MapTokenStore{"tok": "alice"}))(okHandler())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestBearerMiddleware_InvalidToken(t *testing.T) {
	h := auth.BearerMiddleware(auth.WithTokenStore(auth.MapTokenStore{"tok": "alice"}))(okHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer bad")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestBearerMiddleware_ValidToken(t *testing.T) {
	store := auth.MapTokenStore{"sk-abc": "jrose"}
	var got auth.CallerIdentity
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.CallerFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := auth.BearerMiddleware(auth.WithTokenStore(store))(next)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer sk-abc")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got != "jrose" {
		t.Fatalf("want identity jrose, got %q", got)
	}
}

func TestBearerMiddleware_NoStore(t *testing.T) {
	h := auth.BearerMiddleware()(okHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer anything")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

package auth

import (
	"net/http"
	"strings"
)

// TokenStore maps bearer token strings to named identities.
type TokenStore interface {
	// Lookup returns the CallerIdentity for token, or ("", false) when unknown.
	Lookup(token string) (CallerIdentity, bool)
}

// MapTokenStore is a static token→identity map.
type MapTokenStore map[string]CallerIdentity

func (m MapTokenStore) Lookup(token string) (CallerIdentity, bool) {
	id, ok := m[token]
	return id, ok
}

// MiddlewareOption configures BearerMiddleware.
type MiddlewareOption func(*middlewareCfg)

type middlewareCfg struct {
	store TokenStore
}

// WithTokenStore sets the token store used for identity lookup.
func WithTokenStore(s TokenStore) MiddlewareOption {
	return func(c *middlewareCfg) { c.store = s }
}

// BearerMiddleware returns an HTTP middleware that authenticates requests via
// bearer token before passing them to next. A missing or unrecognised token
// returns 401 and halts the chain. Recognised tokens inject CallerIdentity
// into the request context via WithCaller.
func BearerMiddleware(opts ...MiddlewareOption) func(http.Handler) http.Handler {
	cfg := &middlewareCfg{}
	for _, o := range opts {
		o(cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r)
			if token == "" {
				http.Error(w, "unauthenticated: missing bearer token", http.StatusUnauthorized)
				return
			}
			if cfg.store == nil {
				http.Error(w, "unauthenticated: no token store configured", http.StatusUnauthorized)
				return
			}
			identity, ok := cfg.store.Lookup(token)
			if !ok {
				http.Error(w, "unauthenticated: invalid bearer token", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithCaller(r.Context(), identity)))
		})
	}
}

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}

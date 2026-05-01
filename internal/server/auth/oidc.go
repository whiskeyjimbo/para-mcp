package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const (
	defaultCacheTTL  = 5 * time.Minute
	defaultClockSkew = 30 * time.Second
)

// OIDCOption configures OIDCMiddleware.
type OIDCOption func(*oidcCfg)

type oidcCfg struct {
	jwksEndpoint string
	cacheTTL     time.Duration
	clockSkew    time.Duration
}

// WithJWKSEndpoint sets the JWKS endpoint URL.
func WithJWKSEndpoint(url string) OIDCOption {
	return func(c *oidcCfg) { c.jwksEndpoint = url }
}

// WithCacheTTL sets the JWKS cache refresh interval.
func WithCacheTTL(d time.Duration) OIDCOption {
	return func(c *oidcCfg) { c.cacheTTL = d }
}

// WithClockSkew sets the allowed clock skew for JWT expiry checks.
func WithClockSkew(d time.Duration) OIDCOption {
	return func(c *oidcCfg) { c.clockSkew = d }
}

// OIDCMiddleware returns an HTTP middleware that validates OIDC JWTs carried as
// bearer tokens. Identity is sourced from the JWT sub claim and injected via
// WithCaller. Requests with a missing, malformed, or unverifiable JWT are
// rejected 401. The JWKS is fetched from jwksEndpoint and cached; the cache is
// refreshed on TTL expiry or when a key ID is not found. If the cache is cold
// and the endpoint is unreachable, requests are rejected (fail-closed).
func OIDCMiddleware(opts ...OIDCOption) func(http.Handler) http.Handler {
	cfg := &oidcCfg{
		cacheTTL:  defaultCacheTTL,
		clockSkew: defaultClockSkew,
	}
	for _, o := range opts {
		o(cfg)
	}

	cache := jwk.NewCache(context.Background())
	if cfg.jwksEndpoint != "" {
		_ = cache.Register(cfg.jwksEndpoint, jwk.WithMinRefreshInterval(cfg.cacheTTL))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := extractBearer(r)
			if raw == "" {
				http.Error(w, "unauthenticated: missing bearer token", http.StatusUnauthorized)
				return
			}

			if cfg.jwksEndpoint == "" {
				http.Error(w, "unauthenticated: no JWKS endpoint configured", http.StatusUnauthorized)
				return
			}

			keyset, err := cache.Get(r.Context(), cfg.jwksEndpoint)
			if err != nil {
				http.Error(w, "unauthenticated: JWKS unavailable", http.StatusUnauthorized)
				return
			}

			token, err := jwt.Parse([]byte(raw),
				jwt.WithKeySet(keyset, jws.WithInferAlgorithmFromKey(true)),
				jwt.WithValidate(true),
				jwt.WithAcceptableSkew(cfg.clockSkew),
			)
			if err != nil {
				http.Error(w, fmt.Sprintf("unauthenticated: invalid JWT: %v", err), http.StatusUnauthorized)
				return
			}

			sub := token.Subject()
			if sub == "" {
				http.Error(w, "unauthenticated: JWT missing sub claim", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r.WithContext(WithCaller(r.Context(), CallerIdentity(sub))))
		})
	}
}

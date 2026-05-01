package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/whiskeyjimbo/paras/internal/server/auth"
)

// testJWKS spins up an httptest server serving the public half of key as JWKS.
func testJWKS(t *testing.T, key *rsa.PrivateKey) *httptest.Server {
	t.Helper()
	pub, err := jwk.FromRaw(key.Public())
	if err != nil {
		t.Fatalf("jwk.FromRaw: %v", err)
	}
	_ = pub.Set(jwk.AlgorithmKey, jwa.RS256)
	_ = pub.Set(jwk.KeyIDKey, "test-key")
	set := jwk.NewSet()
	_ = set.AddKey(pub)
	body, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("json.Marshal jwks: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func signJWT(t *testing.T, key *rsa.PrivateKey, sub string, expiry time.Time) string {
	t.Helper()
	priv, err := jwk.FromRaw(key)
	if err != nil {
		t.Fatalf("jwk.FromRaw private: %v", err)
	}
	_ = priv.Set(jwk.AlgorithmKey, jwa.RS256)
	_ = priv.Set(jwk.KeyIDKey, "test-key")

	tok, err := jwt.NewBuilder().
		Subject(sub).
		Expiration(expiry).
		IssuedAt(time.Now()).
		Build()
	if err != nil {
		t.Fatalf("jwt.Build: %v", err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, priv))
	if err != nil {
		t.Fatalf("jwt.Sign: %v", err)
	}
	return string(signed)
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return k
}

func TestOIDCMiddleware_MissingToken(t *testing.T) {
	key := newRSAKey(t)
	srv := testJWKS(t, key)
	h := auth.OIDCMiddleware(auth.WithJWKSEndpoint(srv.URL))(okHandler())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestOIDCMiddleware_ValidJWT(t *testing.T) {
	key := newRSAKey(t)
	srv := testJWKS(t, key)
	token := signJWT(t, key, "alice", time.Now().Add(time.Hour))

	var got auth.CallerIdentity
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.CallerFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := auth.OIDCMiddleware(auth.WithJWKSEndpoint(srv.URL))(next)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got != "alice" {
		t.Fatalf("want identity alice, got %q", got)
	}
}

func TestOIDCMiddleware_ExpiredJWT(t *testing.T) {
	key := newRSAKey(t)
	srv := testJWKS(t, key)
	token := signJWT(t, key, "bob", time.Now().Add(-time.Hour))

	h := auth.OIDCMiddleware(
		auth.WithJWKSEndpoint(srv.URL),
		auth.WithClockSkew(0),
	)(okHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestOIDCMiddleware_WrongKey(t *testing.T) {
	key := newRSAKey(t)
	otherKey := newRSAKey(t)
	srv := testJWKS(t, otherKey) // JWKS has a different key
	token := signJWT(t, key, "charlie", time.Now().Add(time.Hour))

	h := auth.OIDCMiddleware(auth.WithJWKSEndpoint(srv.URL))(okHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestOIDCMiddleware_NoEndpoint(t *testing.T) {
	token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.fake"
	h := auth.OIDCMiddleware()(okHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestOIDCMiddleware_UnavailableJWKS_ColdCache(t *testing.T) {
	// Point at a server that immediately closes — cold cache, fail-closed.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	dead.Close()

	key := newRSAKey(t)
	token := signJWT(t, key, "dave", time.Now().Add(time.Hour))
	h := auth.OIDCMiddleware(auth.WithJWKSEndpoint(dead.URL))(okHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 (fail-closed), got %d", w.Code)
	}
}

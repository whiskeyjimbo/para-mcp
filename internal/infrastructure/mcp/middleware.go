package mcp

import (
	"net/http"
	"regexp"

	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"github.com/whiskeyjimbo/paras/internal/infra/remotevault"
)

// requestIDPattern is the canonical format for X-PARA-Request-Id values.
// Must be req_ followed by 26 characters from the ULID alphabet.
var requestIDPattern = regexp.MustCompile(`^req_[0-9A-HJKMNP-TV-Z]{26}$`)

const requestIDHeader = "X-PARA-Request-Id"

// ScopeMemoMiddleware installs a per-request scope memoization slot in the
// context so that MemoScopeResolver resolves scopes at most once per request.
func ScopeMemoMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(ports.WithScopeMemo(r.Context())))
	})
}

// RequestIDMiddleware validates the inbound X-PARA-Request-Id header and,
// when valid, stores it in the request context for propagation to outbound
// calls. A malformed header returns HTTP 400 with an invalid_argument body.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id != "" {
			if !requestIDPattern.MatchString(id) {
				http.Error(w, "invalid_argument: "+requestIDHeader+": malformed request ID", http.StatusBadRequest)
				return
			}
			r = r.WithContext(remotevault.WithRequestID(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}

// Package auth provides HTTP middleware for authenticating inbound server-mode requests.
package auth

import "context"

// CallerIdentity is a named identity resolved from a bearer token.
// It is an authentication artifact only; role resolution is the rbac package's concern.
type CallerIdentity string

type contextKey int

const callerKey contextKey = iota

// WithCaller stores identity in ctx.
func WithCaller(ctx context.Context, id CallerIdentity) context.Context {
	return context.WithValue(ctx, callerKey, id)
}

// CallerFrom retrieves the CallerIdentity stored by WithCaller.
// Returns ("", false) when no identity is present.
func CallerFrom(ctx context.Context) (CallerIdentity, bool) {
	id, ok := ctx.Value(callerKey).(CallerIdentity)
	return id, ok
}

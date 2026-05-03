// Package ctxutil provides shared context key helpers used across transport and infra layers.
package ctxutil

import "context"

type contextKey int

const requestIDKey contextKey = iota

// WithRequestID stores a request ID in ctx for propagation to outbound calls.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext returns the request ID stored in ctx, or "".
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// Package waitforindex provides infrastructure for blocking until a note reaches
// a terminal IndexState, with a configurable timeout and an explainer table for
// non-indexed states.
package waitforindex

import (
	"context"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

const (
	defaultTimeout      = 3000 * time.Millisecond
	maxTimeout          = 10000 * time.Millisecond
	defaultPollInterval = 100 * time.Millisecond
)

// StateFunc fetches the current IndexState for noteID.
type StateFunc func(ctx context.Context, noteID string) (domain.IndexState, error)

// Config controls Wait timing.
type Config struct {
	Timeout      time.Duration
	PollInterval time.Duration
}

// DefaultConfig returns the default wait_for_index configuration.
func DefaultConfig() Config {
	return Config{
		Timeout:      defaultTimeout,
		PollInterval: defaultPollInterval,
	}
}

// ClampTimeout returns t clamped to [1ms, maxTimeout]; zero or negative returns defaultTimeout.
func ClampTimeout(t time.Duration) time.Duration {
	if t <= 0 {
		return defaultTimeout
	}
	if t > maxTimeout {
		return maxTimeout
	}
	return t
}

// Result is returned by Wait.
type Result struct {
	State     domain.IndexState
	Explainer string // non-empty for non-indexed states
	TimedOut  bool   // true when cfg.Timeout elapsed before a terminal state
	Cancelled bool   // true when the parent context was cancelled (distinct from timeout)
}

// Wait polls fn until a terminal IndexState is reached or cfg.Timeout elapses.
// On timeout it returns the last observed state with its explainer, not an error.
// If the parent context is cancelled before a terminal state, Cancelled is set.
func Wait(ctx context.Context, noteID string, fn StateFunc, cfg Config) Result {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}

	// Derive a child context that enforces cfg.Timeout on fn calls themselves.
	tctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	var last domain.IndexState = domain.IndexStatePending

	for {
		state, err := fn(tctx, noteID)
		if err == nil {
			last = state
			if isTerminal(state) {
				return Result{State: state, Explainer: Explainer(state)}
			}
		}

		select {
		case <-tctx.Done():
			if ctx.Err() != nil {
				// Parent context was cancelled — not a timeout.
				return Result{State: last, Explainer: Explainer(last), Cancelled: true}
			}
			return Result{State: last, Explainer: Explainer(last), TimedOut: true}
		case <-ticker.C:
		}
	}
}

// isTerminal reports whether s is a terminal IndexState (no further transitions expected).
func isTerminal(s domain.IndexState) bool {
	switch s {
	case domain.IndexStateIndexed,
		domain.IndexStateFailed,
		domain.IndexStateSkippedShort,
		domain.IndexStateSkippedUserEdited,
		domain.IndexStateTombstoned:
		return true
	}
	return false
}

// Explainer returns a human-readable explanation for non-indexed states.
// Returns an empty string for IndexStateIndexed.
func Explainer(s domain.IndexState) string {
	switch s {
	case domain.IndexStatePending:
		return "Note is queued for indexing and has not yet been processed by the semantic pipeline."
	case domain.IndexStateFailed:
		return "Semantic indexing failed after retries. Check logs for the embedding or summarisation error."
	case domain.IndexStateSkippedShort:
		return "Note body is too short to embed meaningfully and was skipped by the pipeline."
	case domain.IndexStateSkippedUserEdited:
		return "Derived metadata was edited by the user and will not be overwritten by the pipeline."
	case domain.IndexStateTombstoned:
		return "Note has been deleted and its vector records are marked for removal."
	default:
		return ""
	}
}

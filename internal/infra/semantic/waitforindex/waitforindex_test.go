package waitforindex_test

import (
	"context"
	"testing"
	"time"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/infra/semantic/waitforindex"
)

// staticStateFunc always returns the same IndexState.
func staticState(s domain.IndexState) waitforindex.StateFunc {
	return func(_ context.Context, _ string) (domain.IndexState, error) {
		return s, nil
	}
}

// transitionStateFunc returns pending N times, then returns terminal.
func transitionState(n int, terminal domain.IndexState) waitforindex.StateFunc {
	count := 0
	return func(_ context.Context, _ string) (domain.IndexState, error) {
		count++
		if count <= n {
			return domain.IndexStatePending, nil
		}
		return terminal, nil
	}
}

func TestWaitReturnsImmediatelyForIndexed(t *testing.T) {
	result := waitforindex.Wait(context.Background(), "note-1", staticState(domain.IndexStateIndexed), waitforindex.DefaultConfig())
	if result.State != domain.IndexStateIndexed {
		t.Errorf("state = %q, want indexed", result.State)
	}
	if result.TimedOut {
		t.Error("TimedOut should be false for immediate indexed result")
	}
}

func TestWaitBlocksUntilTerminal(t *testing.T) {
	cfg := waitforindex.Config{
		Timeout:      200 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	}
	result := waitforindex.Wait(context.Background(), "note-1", transitionState(3, domain.IndexStateIndexed), cfg)
	if result.State != domain.IndexStateIndexed {
		t.Errorf("state = %q, want indexed", result.State)
	}
	if result.TimedOut {
		t.Error("should not time out when terminal reached in time")
	}
}

func TestWaitTimeoutReturnsCurrentState(t *testing.T) {
	cfg := waitforindex.Config{
		Timeout:      20 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	}
	// Always pending — will time out.
	result := waitforindex.Wait(context.Background(), "note-1", staticState(domain.IndexStatePending), cfg)
	if result.State != domain.IndexStatePending {
		t.Errorf("state = %q, want pending on timeout", result.State)
	}
	if !result.TimedOut {
		t.Error("TimedOut should be true after timeout")
	}
	if result.Explainer == "" {
		t.Error("Explainer should be non-empty on timeout")
	}
}

func TestExplainerNonEmptyForAllNonIndexedStates(t *testing.T) {
	nonIndexed := []domain.IndexState{
		domain.IndexStatePending,
		domain.IndexStateFailed,
		domain.IndexStateSkippedShort,
		domain.IndexStateSkippedUserEdited,
		domain.IndexStateTombstoned,
	}
	for _, s := range nonIndexed {
		if got := waitforindex.Explainer(s); got == "" {
			t.Errorf("Explainer(%q) is empty, want non-empty string", s)
		}
	}
}

func TestExplainerEmptyForIndexed(t *testing.T) {
	if got := waitforindex.Explainer(domain.IndexStateIndexed); got != "" {
		t.Errorf("Explainer(indexed) = %q, want empty string", got)
	}
}

func TestClampTimeout(t *testing.T) {
	cases := []struct {
		input time.Duration
		want  time.Duration
	}{
		{0, 3000 * time.Millisecond},                         // zero → default
		{-1, 3000 * time.Millisecond},                        // negative → default
		{500 * time.Millisecond, 500 * time.Millisecond},     // within range → unchanged
		{15000 * time.Millisecond, 10000 * time.Millisecond}, // over max → clamped
	}
	for _, c := range cases {
		got := waitforindex.ClampTimeout(c.input)
		if got != c.want {
			t.Errorf("ClampTimeout(%v) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestWaitAllTerminalStates(t *testing.T) {
	terminal := []domain.IndexState{
		domain.IndexStateIndexed,
		domain.IndexStateFailed,
		domain.IndexStateSkippedShort,
		domain.IndexStateSkippedUserEdited,
		domain.IndexStateTombstoned,
	}
	for _, s := range terminal {
		cfg := waitforindex.Config{Timeout: 100 * time.Millisecond, PollInterval: 5 * time.Millisecond}
		result := waitforindex.Wait(context.Background(), "n", staticState(s), cfg)
		if result.State != s {
			t.Errorf("state = %q, want %q", result.State, s)
		}
		if result.TimedOut {
			t.Errorf("state %q should be terminal, got TimedOut=true", s)
		}
	}
}

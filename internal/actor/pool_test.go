package actor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPool_SerializesPerPath(t *testing.T) {
	p := New()
	defer p.Close()

	var seq []int
	var mu sync.Mutex

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)

	for i := range n {
		go func() {
			defer wg.Done()
			_ = p.Do(context.Background(), "personal", "projects/foo.md", func() error {
				mu.Lock()
				seq = append(seq, i)
				mu.Unlock()
				return nil
			})
		}()
	}
	wg.Wait()

	if len(seq) != n {
		t.Fatalf("expected %d operations, got %d", n, len(seq))
	}
}

func TestPool_DifferentPathsRunConcurrently(t *testing.T) {
	p := New()
	defer p.Close()

	// Two paths should not block each other.
	var started int32
	block := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_ = p.Do(context.Background(), "personal", "projects/a.md", func() error {
			atomic.AddInt32(&started, 1)
			<-block
			return nil
		})
	}()

	// Wait until the first op has started.
	for atomic.LoadInt32(&started) == 0 {
		time.Sleep(time.Millisecond)
	}

	// Second path must not be blocked by the first.
	done := make(chan struct{})
	go func() {
		defer wg.Done()
		_ = p.Do(context.Background(), "personal", "projects/b.md", func() error {
			return nil
		})
		close(done)
	}()

	select {
	case <-done:
		// good: different paths ran concurrently
	case <-time.After(2 * time.Second):
		t.Fatal("different-path operation was blocked by unrelated path")
	}

	close(block)
	wg.Wait()
}

func TestPool_ScopeSeparation(t *testing.T) {
	p := New()
	defer p.Close()

	// same path in different scopes must be independent actors
	var wg sync.WaitGroup
	wg.Add(2)

	results := make([]string, 0, 2)
	var mu sync.Mutex

	for _, scope := range []string{"personal", "team-platform"} {
		go func() {
			defer wg.Done()
			_ = p.Do(context.Background(), scope, "projects/shared.md", func() error {
				mu.Lock()
				results = append(results, scope)
				mu.Unlock()
				return nil
			})
		}()
	}
	wg.Wait()
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestPool_PropagatesError(t *testing.T) {
	p := New()
	defer p.Close()

	want := errors.New("write failed")
	got := p.Do(context.Background(), "personal", "projects/x.md", func() error {
		return want
	})
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPool_ContextCancellation(t *testing.T) {
	p := New()
	defer p.Close()

	block := make(chan struct{})
	// Fill the actor with a blocking op.
	go func() {
		_ = p.Do(context.Background(), "personal", "projects/z.md", func() error {
			<-block
			return nil
		})
	}()
	time.Sleep(5 * time.Millisecond) // let the blocking op start

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.Do(ctx, "personal", "projects/z.md", func() error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	close(block)
}

func TestPool_ClosedPoolReturnsError(t *testing.T) {
	p := New()
	p.Close()

	err := p.Do(context.Background(), "personal", "projects/x.md", func() error { return nil })
	if err != ErrPoolClosed {
		t.Fatalf("expected ErrPoolClosed, got %v", err)
	}
}

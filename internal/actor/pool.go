// Package actor provides a daemon-global per-path actor pool that serializes
// all mutations to a single note path without file-level OS locks.
package actor

import (
	"context"
	"sync"
)

// key identifies a single note by scope and vault-relative path.
type key struct {
	scope string
	path  string
}

// op is a unit of work sent to an actor goroutine.
type op struct {
	fn   func() error
	done chan<- error
}

// Pool is a daemon-global registry of per-(scope,path) actor goroutines.
// Each unique (scope, path) pair gets exactly one goroutine that processes
// operations sequentially, so concurrent callers writing the same note are
// automatically serialized without file-level OS locks.
//
// Actors are created lazily on first use and live until Close is called.
// Pool must not be copied after first use.
type Pool struct {
	// mu guards actors and closed.
	// Do holds RLock for the entire channel-send so Close cannot close a
	// channel while a send is in progress.
	mu     sync.RWMutex
	actors map[key]chan op
	closed bool
	wg     sync.WaitGroup
}

// New returns a ready Pool.
func New() *Pool {
	return &Pool{actors: make(map[key]chan op)}
}

// Do sends fn to the actor for (scope, path) and waits for it to complete.
// Returns fn's error, or ctx.Err() if the context is cancelled before the
// operation is dispatched.
func (p *Pool) Do(ctx context.Context, scope, path string, fn func() error) error {
	k := key{scope, path}

	// Fast path: actor already exists.
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return ErrPoolClosed
	}
	ch, ok := p.actors[k]
	if !ok {
		p.mu.RUnlock()
		ch = p.create(k)
		if ch == nil {
			return ErrPoolClosed
		}
		// Re-acquire RLock so the channel-send is protected against Close.
		p.mu.RLock()
		if p.closed {
			p.mu.RUnlock()
			return ErrPoolClosed
		}
	}

	// Send while holding RLock — Close cannot run concurrently.
	done := make(chan error, 1)
	var sendErr error
	select {
	case ch <- op{fn: fn, done: done}:
	case <-ctx.Done():
		sendErr = ctx.Err()
	}
	p.mu.RUnlock()
	if sendErr != nil {
		return sendErr
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close shuts down all actor goroutines. In-flight sends complete before any
// channel is closed. Close blocks until all actors have exited.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	channels := make([]chan op, 0, len(p.actors))
	for _, ch := range p.actors {
		channels = append(channels, ch)
	}
	p.mu.Unlock()
	// No Do can send after this point: new Dos see closed=true; any Do that
	// held RLock during the Lock() call has already completed its send.

	for _, ch := range channels {
		close(ch)
	}
	p.wg.Wait()
}

// create acquires a write lock to lazily start the actor for k.
// Returns nil if the pool was closed while acquiring the lock.
func (p *Pool) create(k key) chan op {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if ch, ok := p.actors[k]; ok {
		return ch // another goroutine created it first
	}
	ch := make(chan op, 64)
	p.actors[k] = ch
	p.wg.Go(func() {
		for o := range ch {
			o.done <- o.fn()
		}
	})
	return ch
}

// closedError is the sentinel for calls after Close.
type closedError struct{}

func (closedError) Error() string { return "actor pool is closed" }

// ErrPoolClosed is returned by Do when called on a closed Pool.
var ErrPoolClosed error = closedError{}

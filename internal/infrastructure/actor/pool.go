// Package actor provides a daemon-global per-path actor pool that serializes
// all mutations to a single note path without file-level OS locks.
package actor

import (
	"context"
	"errors"
	"sync"
)

type key struct {
	scope string
	path  string
}

type op struct {
	fn   func() error
	done chan<- error
}

// Pool is a daemon-global registry of per-(scope,path) actor goroutines.
// Each unique (scope, path) pair gets exactly one goroutine that processes
// operations sequentially, so concurrent callers writing the same note are
// automatically serialized without file-level OS locks.
type Pool struct {
	mu         sync.RWMutex
	actors     map[key]chan op
	closed     bool
	wg         sync.WaitGroup
	bufferSize int
}

// Option configures a Pool.
type Option func(*Pool)

// WithBufferSize sets the per-actor channel buffer size (default 64).
func WithBufferSize(n int) Option {
	return func(p *Pool) { p.bufferSize = n }
}

func New(opts ...Option) *Pool {
	p := &Pool{actors: make(map[key]chan op), bufferSize: 64}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Do sends fn to the actor for (scope, path) and waits for it to complete.
func (p *Pool) Do(ctx context.Context, scope, path string, fn func() error) error {
	k := key{scope, path}

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
		p.mu.RLock()
		if p.closed {
			p.mu.RUnlock()
			return ErrPoolClosed
		}
	}

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

// Close shuts down all actor goroutines.
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

	for _, ch := range channels {
		close(ch)
	}
	p.wg.Wait()
}

func (p *Pool) create(k key) chan op {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if ch, ok := p.actors[k]; ok {
		return ch
	}
	ch := make(chan op, p.bufferSize)
	p.actors[k] = ch
	p.wg.Go(func() {
		for o := range ch {
			o.done <- o.fn()
		}
	})
	return ch
}

// ErrPoolClosed is returned by Do when called on a closed Pool.
var ErrPoolClosed = errors.New("actor pool is closed")

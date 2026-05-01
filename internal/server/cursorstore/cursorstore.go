// Package cursorstore defines the CursorStore port and provides
// in-memory, Postgres, and Redis implementations for multi-replica
// cursor handle persistence.
package cursorstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Cursor holds the serialisable state for a pagination handle.
type Cursor struct {
	Data []byte // opaque JSON blob (cursorPayload from application layer)
}

// CursorStore persists pagination cursor handles across requests and replicas.
type CursorStore interface {
	Get(ctx context.Context, id string) (Cursor, error)
	Put(ctx context.Context, id string, c Cursor, ttl time.Duration) error
	Delete(ctx context.Context, id string) error
}

// --- In-memory implementation ---

const defaultMaxEntries = 10_000

type memEntry struct {
	cursor Cursor
	expiry time.Time
}

// InMemory is a single-replica in-memory CursorStore. Safe for concurrent use.
type InMemory struct {
	mu      sync.Mutex
	entries map[string]memEntry
	max     int
}

// InMemoryOption configures an InMemory store.
type InMemoryOption func(*InMemory)

// WithMaxEntries sets the maximum number of live handles (default 10 000).
func WithMaxEntries(n int) InMemoryOption {
	return func(s *InMemory) { s.max = n }
}

// NewInMemory returns an in-memory CursorStore suitable for single-node deploys.
func NewInMemory(opts ...InMemoryOption) *InMemory {
	s := &InMemory{entries: make(map[string]memEntry), max: defaultMaxEntries}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *InMemory) Get(_ context.Context, id string) (Cursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok || time.Now().After(e.expiry) {
		delete(s.entries, id)
		return Cursor{}, fmt.Errorf("cursor not found or expired: %s", id)
	}
	return e.cursor, nil
}

func (s *InMemory) Put(_ context.Context, id string, c Cursor, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= s.max {
		now := time.Now()
		for k, e := range s.entries {
			if now.After(e.expiry) {
				delete(s.entries, k)
			}
		}
	}
	s.entries[id] = memEntry{cursor: c, expiry: time.Now().Add(ttl)}
	return nil
}

func (s *InMemory) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	delete(s.entries, id)
	s.mu.Unlock()
	return nil
}

// --- Postgres implementation ---

// Postgres persists cursor handles in a Postgres table for multi-replica use.
// Required DDL:
//
//	CREATE TABLE cursor_handles (
//	  id         TEXT PRIMARY KEY,
//	  data       JSONB NOT NULL,
//	  expires_at TIMESTAMPTZ NOT NULL
//	);
//	CREATE INDEX ON cursor_handles(expires_at);
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres creates a Postgres-backed CursorStore using the given pool.
func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func (p *Postgres) Get(ctx context.Context, id string) (Cursor, error) {
	var data []byte
	err := p.pool.QueryRow(ctx,
		`SELECT data FROM cursor_handles WHERE id = $1 AND expires_at > now()`, id,
	).Scan(&data)
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor not found or expired: %s: %w", id, err)
	}
	return Cursor{Data: data}, nil
}

func (p *Postgres) Put(ctx context.Context, id string, c Cursor, ttl time.Duration) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO cursor_handles (id, data, expires_at)
		 VALUES ($1, $2, now() + $3::interval)
		 ON CONFLICT (id) DO UPDATE SET data = excluded.data, expires_at = excluded.expires_at`,
		id, json.RawMessage(c.Data), ttl.String(),
	)
	return err
}

func (p *Postgres) Delete(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM cursor_handles WHERE id = $1`, id)
	return err
}

// --- Redis implementation ---

// Redis persists cursor handles in Redis for multi-replica use.
type Redis struct {
	client *redis.Client
}

// NewRedis creates a Redis-backed CursorStore using the given client.
func NewRedis(client *redis.Client) *Redis {
	return &Redis{client: client}
}

func (r *Redis) Get(ctx context.Context, id string) (Cursor, error) {
	val, err := r.client.Get(ctx, cursorKey(id)).Bytes()
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor not found or expired: %s: %w", id, err)
	}
	return Cursor{Data: val}, nil
}

func (r *Redis) Put(ctx context.Context, id string, c Cursor, ttl time.Duration) error {
	return r.client.Set(ctx, cursorKey(id), c.Data, ttl).Err()
}

func (r *Redis) Delete(ctx context.Context, id string) error {
	return r.client.Del(ctx, cursorKey(id)).Err()
}

func cursorKey(id string) string { return "cursor:" + id }

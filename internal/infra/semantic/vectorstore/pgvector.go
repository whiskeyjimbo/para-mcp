// Package vectorstore provides VectorStore port implementations for pgvector and sqlite-vec.
package vectorstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
)

var _ ports.VectorStore = (*PgVectorStore)(nil)

// PgVectorConfig configures the pgvector backend.
type PgVectorConfig struct {
	DSN  string
	Dims int
}

// PgVectorStore implements the VectorStore port backed by pgvector.
type PgVectorStore struct {
	pool *pgxpool.Pool
	dims int
}

const pgvectorDDL = `
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS vector_records (
    id          TEXT        NOT NULL,
    scope       TEXT        NOT NULL,
    path        TEXT        NOT NULL,
    chunk       INT         NOT NULL DEFAULT 0,
    body        TEXT        NOT NULL DEFAULT '',
    embedding   vector(%d),
    tombstoned  BOOLEAN     NOT NULL DEFAULT FALSE,
    PRIMARY KEY (id, chunk)
);
CREATE INDEX IF NOT EXISTS vector_records_scope ON vector_records (scope);
CREATE INDEX IF NOT EXISTS vector_records_embedding ON vector_records USING hnsw (embedding vector_cosine_ops);
`

// NewPgVector opens a pool, creates the schema, and returns a PgVectorStore.
func NewPgVector(ctx context.Context, cfg PgVectorConfig) (*PgVectorStore, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("pgvector: connect: %w", err)
	}
	ddl := fmt.Sprintf(pgvectorDDL, cfg.Dims)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgvector: migrate: %w", err)
	}
	return &PgVectorStore{pool: pool, dims: cfg.Dims}, nil
}

// Close closes the connection pool.
func (s *PgVectorStore) Close() error {
	s.pool.Close()
	return nil
}

// Upsert inserts or replaces vector records.
func (s *PgVectorStore) Upsert(ctx context.Context, records []domain.VectorRecord) error {
	for _, r := range records {
		_, err := s.pool.Exec(ctx, `
INSERT INTO vector_records (id, scope, path, chunk, body, embedding, tombstoned)
VALUES ($1, $2, $3, $4, $5, $6::vector, FALSE)
ON CONFLICT (id, chunk) DO UPDATE SET
    scope      = EXCLUDED.scope,
    path       = EXCLUDED.path,
    body       = EXCLUDED.body,
    embedding  = EXCLUDED.embedding,
    tombstoned = FALSE
`, r.ID, r.Ref.Scope, r.Ref.Path, r.Chunk, r.Body, pgVector(r.Vector))
		if err != nil {
			return fmt.Errorf("pgvector upsert %q: %w", r.ID, err)
		}
	}
	return nil
}

// Search performs an approximate nearest-neighbour search with AllowedScopes pre-filter.
// nil AllowedScopes is a programmer error and returns an error.
// Empty AllowedScopes returns an empty result (deny-all).
func (s *PgVectorStore) Search(ctx context.Context, query []float32, filter domain.AuthFilter, k int) ([]domain.VectorHit, error) {
	if filter.AllowedScopes == nil {
		return nil, errors.New("pgvector: nil AllowedScopes is a programmer error")
	}
	if len(filter.AllowedScopes) == 0 {
		return nil, nil
	}

	// DISTINCT ON deduplicates chunk hits: keep the best chunk per (id, scope, path).
	rows, err := s.pool.Query(ctx, `
SELECT DISTINCT ON (id, scope, path)
    id, scope, path, chunk, body,
    1 - (embedding <=> $1::vector) AS score
FROM vector_records
WHERE scope = ANY($2)
  AND tombstoned = FALSE
ORDER BY id, scope, path, score DESC
LIMIT $3
`, pgVector(query), filter.AllowedScopes, k)
	if err != nil {
		return nil, fmt.Errorf("pgvector search: %w", err)
	}
	defer rows.Close()

	var hits []domain.VectorHit
	for rows.Next() {
		var h domain.VectorHit
		if err := rows.Scan(&h.ID, &h.Ref.Scope, &h.Ref.Path, &h.Chunk, &h.Body, &h.Score); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// Delete removes records by ID.
func (s *PgVectorStore) Delete(ctx context.Context, ids []string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM vector_records WHERE id = ANY($1)`, ids)
	return err
}

// Tombstone marks records as deleted (soft-delete; swept later).
func (s *PgVectorStore) Tombstone(ctx context.Context, ids []string) error {
	_, err := s.pool.Exec(ctx, `UPDATE vector_records SET tombstoned = TRUE WHERE id = ANY($1)`, ids)
	return err
}

// pgVector formats a float32 slice as a pgvector literal string.
func pgVector(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	b := make([]byte, 0, len(v)*8+2)
	b = append(b, '[')
	for i, f := range v {
		if i > 0 {
			b = append(b, ',')
		}
		b = fmt.Appendf(b, "%g", f)
	}
	b = append(b, ']')
	return string(b)
}

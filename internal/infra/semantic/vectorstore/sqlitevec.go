package vectorstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
)

var _ ports.VectorStore = (*SqliteVecStore)(nil)

// SqliteVecConfig configures the sqlite-vec backend.
type SqliteVecConfig struct {
	DSN  string // e.g. ":memory:" or file path
	Dims int
}

// SqliteVecStore implements the VectorStore port backed by sqlite-vec.
type SqliteVecStore struct {
	db   *sql.DB
	dims int
}

const sqliteVecDDL = `
CREATE VIRTUAL TABLE IF NOT EXISTS vec_records USING vec0(
    id TEXT PRIMARY KEY,
    embedding float[%d]
);
CREATE TABLE IF NOT EXISTS vec_meta (
    id         TEXT    NOT NULL,
    scope      TEXT    NOT NULL,
    path       TEXT    NOT NULL,
    chunk      INT     NOT NULL DEFAULT 0,
    body       TEXT    NOT NULL DEFAULT '',
    tombstoned INT     NOT NULL DEFAULT 0,
    PRIMARY KEY (id, chunk)
);
`

// NewSqliteVec opens a sqlite-vec database, creates the schema, and returns a SqliteVecStore.
func NewSqliteVec(ctx context.Context, cfg SqliteVecConfig) (*SqliteVecStore, error) {
	db, err := sql.Open("sqlite3", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("sqlite-vec: open: %w", err)
	}
	ddl := fmt.Sprintf(sqliteVecDDL, cfg.Dims)
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite-vec: migrate: %w", err)
	}
	return &SqliteVecStore{db: db, dims: cfg.Dims}, nil
}

// Close closes the database.
func (s *SqliteVecStore) Close() error { return s.db.Close() }

// Upsert inserts or replaces vector records.
func (s *SqliteVecStore) Upsert(ctx context.Context, records []domain.VectorRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, r := range records {
		vecBytes, err := sqlitevec.SerializeFloat32(r.Vector)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO vec_meta (id, scope, path, chunk, body, tombstoned)
VALUES (?, ?, ?, ?, ?, 0)
ON CONFLICT(id, chunk) DO UPDATE SET
    scope      = excluded.scope,
    path       = excluded.path,
    body       = excluded.body,
    tombstoned = 0
`, r.ID, r.Ref.Scope, r.Ref.Path, r.Chunk, r.Body); err != nil {
			return fmt.Errorf("sqlite-vec upsert meta %q: %w", r.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO vec_records (id, embedding)
VALUES (?, ?)
ON CONFLICT(id) DO UPDATE SET embedding = excluded.embedding
`, r.ID, vecBytes); err != nil {
			return fmt.Errorf("sqlite-vec upsert vec %q: %w", r.ID, err)
		}
	}
	return tx.Commit()
}

// Search performs a KNN search with AllowedScopes pre-filter.
// nil AllowedScopes returns an error; empty AllowedScopes returns empty result.
func (s *SqliteVecStore) Search(ctx context.Context, query []float32, filter domain.AuthFilter, k int) ([]domain.VectorHit, error) {
	if filter.AllowedScopes == nil {
		return nil, errors.New("sqlite-vec: nil AllowedScopes is a programmer error")
	}
	if len(filter.AllowedScopes) == 0 {
		return nil, nil
	}

	qBytes, err := sqlitevec.SerializeFloat32(query)
	if err != nil {
		return nil, err
	}

	scopePH, scopeArgs := inList(filter.AllowedScopes)
	// KNN via vec0, then join and GROUP BY to dedup chunks per note.
	querySQL := fmt.Sprintf(`
SELECT m.id, m.scope, m.path, MIN(m.chunk) AS chunk, m.body, v.distance
FROM vec_records v
JOIN vec_meta m ON v.id = m.id
WHERE v.embedding MATCH ? AND k = ?
  AND m.scope IN (%s)
  AND m.tombstoned = 0
GROUP BY m.id, m.scope, m.path
ORDER BY v.distance ASC
LIMIT ?
`, scopePH)
	args := append([]any{qBytes, k}, scopeArgs...)
	args = append(args, k)

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite-vec search: %w", err)
	}
	defer rows.Close()

	var hits []domain.VectorHit
	for rows.Next() {
		var h domain.VectorHit
		var dist float64
		if err := rows.Scan(&h.ID, &h.Ref.Scope, &h.Ref.Path, &h.Chunk, &h.Body, &dist); err != nil {
			return nil, err
		}
		h.Score = 1 - dist
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// Delete removes records by ID.
func (s *SqliteVecStore) Delete(ctx context.Context, ids []string) error {
	ph, args := inList(ids)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM vec_meta WHERE id IN (%s)`, ph), args...); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM vec_records WHERE id IN (%s)`, ph), args...); err != nil {
		return err
	}
	return nil
}

// Tombstone marks records as soft-deleted.
func (s *SqliteVecStore) Tombstone(ctx context.Context, ids []string) error {
	ph, args := inList(ids)
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE vec_meta SET tombstoned = 1 WHERE id IN (%s)`, ph), args...)
	return err
}

// ListTombstoned returns up to limit IDs of soft-deleted records for the sweeper.
func (s *SqliteVecStore) ListTombstoned(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT id FROM vec_meta WHERE tombstoned = 1 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// inList builds SQL IN placeholders and args for a slice.
func inList[T any](items []T) (string, []any) {
	args := make([]any, len(items))
	ph := make([]byte, 0, len(items)*2)
	for i, v := range items {
		if i > 0 {
			ph = append(ph, ',')
		}
		ph = append(ph, '?')
		args[i] = v
	}
	return string(ph), args
}

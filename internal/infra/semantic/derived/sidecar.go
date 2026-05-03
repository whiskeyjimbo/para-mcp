package derived

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

var _ Store = (*SidecarStore)(nil)

// SidecarStore persists DerivedMetadata in a PostgreSQL table.
// It never modifies user markdown files.
type SidecarStore struct {
	pool *pgxpool.Pool
}

const sidecarDDL = `
CREATE TABLE IF NOT EXISTS derived_metadata (
    note_id         TEXT        PRIMARY KEY,
    scope           TEXT        NOT NULL,
    path            TEXT        NOT NULL,
    data            JSONB       NOT NULL,
    schema_version  INT         NOT NULL,
    edited_by_user  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL
);
`

// NewSidecarStore opens a pool, creates the table if needed, and returns a SidecarStore.
func NewSidecarStore(ctx context.Context, dsn string) (*SidecarStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("sidecar store: connect: %w", err)
	}
	if _, err := pool.Exec(ctx, sidecarDDL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("sidecar store: migrate: %w", err)
	}
	return &SidecarStore{pool: pool}, nil
}

// Close closes the underlying connection pool.
func (s *SidecarStore) Close() { s.pool.Close() }

// GetByRef retrieves DerivedMetadata by scope + path.
func (s *SidecarStore) GetByRef(ctx context.Context, ref domain.NoteRef) (*domain.DerivedMetadata, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT data FROM derived_metadata WHERE scope = $1 AND path = $2`, ref.Scope, ref.Path)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return nil, fmt.Errorf("sidecar get-by-ref %s: %w", ref, mapPgNotFound(err))
	}
	var meta domain.DerivedMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("sidecar parse %s: %w", ref, err)
	}
	return &meta, nil
}

// Get retrieves DerivedMetadata by noteID.
func (s *SidecarStore) Get(ctx context.Context, noteID string) (*domain.DerivedMetadata, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT data FROM derived_metadata WHERE note_id = $1`, noteID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return nil, fmt.Errorf("sidecar get %q: %w", noteID, mapPgNotFound(err))
	}
	var meta domain.DerivedMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("sidecar parse %q: %w", noteID, err)
	}
	return &meta, nil
}

// Set upserts DerivedMetadata for the given noteID. EditedByUser=true is preserved
// across pipeline writes — the pipeline must not overwrite user-authored fields.
func (s *SidecarStore) Set(ctx context.Context, noteID string, ref domain.NoteRef, meta *domain.DerivedMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.pool.Exec(ctx, `
INSERT INTO derived_metadata (note_id, scope, path, data, schema_version, edited_by_user, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
ON CONFLICT (note_id) DO UPDATE SET
    data           = EXCLUDED.data,
    schema_version = EXCLUDED.schema_version,
    edited_by_user = EXCLUDED.edited_by_user,
    updated_at     = EXCLUDED.updated_at
`,
		noteID, ref.Scope, ref.Path, data, meta.SchemaVersion, meta.EditedByUser, now,
	)
	return err
}

// IsEditedByUser returns true if the note has been user-edited.
func (s *SidecarStore) IsEditedByUser(ctx context.Context, noteID string) (bool, error) {
	var edited bool
	err := s.pool.QueryRow(ctx,
		`SELECT edited_by_user FROM derived_metadata WHERE note_id = $1`, noteID,
	).Scan(&edited)
	if err != nil {
		return false, mapPgNotFound(err)
	}
	return edited, nil
}

func mapPgNotFound(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

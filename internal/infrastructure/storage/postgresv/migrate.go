package postgresv

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// built-in schema DDL applied when no migration directory is configured.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS notes (
    scope       TEXT        NOT NULL,
    path        TEXT        NOT NULL,
    body        TEXT        NOT NULL DEFAULT '',
    etag        TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    deleted_at  TIMESTAMPTZ,
    -- structured front matter fields (duplicated for SQL filtering)
    title       TEXT        NOT NULL DEFAULT '',
    tags        TEXT[]      NOT NULL DEFAULT '{}',
    status      TEXT        NOT NULL DEFAULT '',
    area        TEXT        NOT NULL DEFAULT '',
    project     TEXT        NOT NULL DEFAULT '',
    fm_extra    JSONB       NOT NULL DEFAULT '{}',
    PRIMARY KEY (scope, path)
);
CREATE INDEX IF NOT EXISTS notes_scope_updated ON notes (scope, updated_at DESC);
CREATE INDEX IF NOT EXISTS notes_deleted_at ON notes (deleted_at) WHERE deleted_at IS NULL;
`

func (v *PostgresVault) migrate(ctx context.Context, dir string) error {
	if dir == "" {
		_, err := v.pool.Exec(ctx, schemaDDL)
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migration dir: %w", err)
	}
	var sqlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			sqlFiles = append(sqlFiles, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(sqlFiles)
	for _, f := range sqlFiles {
		data, err := fs.ReadFile(os.DirFS(filepath.Dir(f)), filepath.Base(f))
		if err != nil {
			return fmt.Errorf("read %s: %w", f, err)
		}
		if _, err := v.pool.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("exec %s: %w", f, err)
		}
	}
	return nil
}

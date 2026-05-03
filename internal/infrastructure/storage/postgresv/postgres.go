// Package postgresv implements the Vault port backed by a Postgres database.
// Multi-writer safety is provided by SELECT FOR UPDATE row locks inside transactions.
package postgresv

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/index"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/noteutil"
)

// Option configures a PostgresVault.
type Option func(*vaultConfig)

type vaultConfig struct {
	dsn          string
	maxConns     int32
	migrationDir string
	log          *slog.Logger
	clock        noteutil.Clock
	ftsIndex     ports.FTSIndex
}

// WithDSN sets the Postgres connection string (default: $DATABASE_URL).
func WithDSN(dsn string) Option {
	return func(c *vaultConfig) { c.dsn = dsn }
}

// WithMaxConns sets the maximum pool size (default: 10).
func WithMaxConns(n int32) Option {
	return func(c *vaultConfig) { c.maxConns = n }
}

// WithMigrationDir sets a directory of *.sql files to run in lexical order on startup.
// When empty, the built-in CREATE TABLE IF NOT EXISTS schema is applied instead.
func WithMigrationDir(dir string) Option {
	return func(c *vaultConfig) { c.migrationDir = dir }
}

// WithLogger sets the logger (default: slog.Default()).
func WithLogger(l *slog.Logger) Option {
	return func(c *vaultConfig) { c.log = l }
}

// WithClock overrides the time source (default: time.Now().UTC()).
func WithClock(fn noteutil.Clock) Option {
	return func(c *vaultConfig) { c.clock = fn }
}

// WithFTSIndex replaces the default BM25 index with a custom implementation.
func WithFTSIndex(i ports.FTSIndex) Option {
	return func(c *vaultConfig) { c.ftsIndex = i }
}

// PostgresVault is a Postgres-backed implementation of ports.Vault.
type PostgresVault struct {
	scope string
	caps  domain.Capabilities
	clock noteutil.Clock
	log   *slog.Logger

	pool  *pgxpool.Pool
	idx   ports.FTSIndex
	cache *noteutil.NoteCache
	graph *noteutil.BacklinkGraph
}

// New creates and initializes a PostgresVault.
// It runs schema migration and loads all existing notes into the in-memory indexes.
func New(ctx context.Context, scope string, opts ...Option) (*PostgresVault, error) {
	cfg := &vaultConfig{
		maxConns: 10,
		log:      slog.Default(),
		clock:    noteutil.DefaultClock,
	}
	for _, o := range opts {
		o(cfg)
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.dsn)
	if err != nil {
		return nil, fmt.Errorf("postgresv: parse DSN: %w", err)
	}
	poolCfg.MaxConns = cfg.maxConns

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgresv: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgresv: ping: %w", err)
	}

	v := &PostgresVault{
		scope: scope,
		caps: domain.Capabilities{
			Writable:   true,
			SoftDelete: true,
		},
		clock: cfg.clock,
		log:   cfg.log,
		pool:  pool,
		cache: noteutil.NewNoteCache(),
		graph: noteutil.NewBacklinkGraph(),
	}

	if cfg.ftsIndex != nil {
		v.idx = cfg.ftsIndex
	} else {
		v.idx = index.New()
	}

	if err := v.migrate(ctx, cfg.migrationDir); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgresv: migrate: %w", err)
	}

	if err := v.loadAll(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgresv: initial load: %w", err)
	}

	return v, nil
}

// Close releases the pool and FTS index.
func (v *PostgresVault) Close() error {
	v.pool.Close()
	v.idx.Close()
	return nil
}

// Scope returns the vault scope identifier.
func (v *PostgresVault) Scope() domain.ScopeID { return v.scope }

// Capabilities returns the vault's capability flags.
func (v *PostgresVault) Capabilities() domain.Capabilities { return v.caps }

// Verify PostgresVault satisfies the Vault interface at compile time.
var _ interface {
	ports.Vault
	io.Closer
} = (*PostgresVault)(nil)

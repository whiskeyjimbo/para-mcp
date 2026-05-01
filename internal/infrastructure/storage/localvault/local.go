// Package localvault implements the filesystem-backed Vault adapter.
package localvault

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/actor"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/index"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/noteutil"
)

// Option configures a LocalVault.
type Option func(*vaultConfig)

type vaultConfig struct {
	indexOpts        []index.Option
	ftsIndex         ports.FTSIndex
	executor         actor.Executor
	log              *slog.Logger
	clock            func() time.Time
	conflictPatterns []*regexp.Regexp
	actorOpts        []actor.Option
	rescanInterval   time.Duration
	renamePairWindow time.Duration
}

// WithIndexOptions passes options through to the default BM25 index.
// Ignored if WithFTSIndex is also provided.
func WithIndexOptions(opts ...index.Option) Option {
	return func(c *vaultConfig) { c.indexOpts = append(c.indexOpts, opts...) }
}

// WithFTSIndex replaces the default BM25 index with a custom implementation.
func WithFTSIndex(i ports.FTSIndex) Option {
	return func(c *vaultConfig) { c.ftsIndex = i }
}

// WithClock overrides the time source for note timestamps (default: time.Now).
func WithClock(fn func() time.Time) Option {
	return func(c *vaultConfig) { c.clock = fn }
}

// WithConflictPatterns overrides the set of filename patterns the watcher
// treats as sync-conflict or OS-metadata files (default: DefaultConflictPatterns).
func WithConflictPatterns(patterns []*regexp.Regexp) Option {
	return func(c *vaultConfig) { c.conflictPatterns = patterns }
}

// WithActorOptions passes options through to the default actor pool (e.g. WithBufferSize).
// Ignored if WithActorExecutor is also provided.
func WithActorOptions(opts ...actor.Option) Option {
	return func(c *vaultConfig) { c.actorOpts = append(c.actorOpts, opts...) }
}

// WithActorExecutor replaces the default actor pool with a custom Executor.
func WithActorExecutor(e actor.Executor) Option {
	return func(c *vaultConfig) { c.executor = e }
}

// WithLogger sets the logger used by the watcher (default: slog.Default()).
func WithLogger(l *slog.Logger) Option {
	return func(c *vaultConfig) { c.log = l }
}

// WithRescanInterval sets how often the watcher triggers a full vault rescan
// (default: 60s).
func WithRescanInterval(d time.Duration) Option {
	return func(c *vaultConfig) { c.rescanInterval = d }
}

// WithRenamePairWindow sets how long the watcher waits to pair a REMOVE event
// with a CREATE before treating it as a deletion (default: 50ms).
func WithRenamePairWindow(d time.Duration) Option {
	return func(c *vaultConfig) { c.renamePairWindow = d }
}

// LocalVault is a filesystem-backed implementation of ports.Vault.
type LocalVault struct {
	scope string
	root  string
	caps  domain.Capabilities
	clock func() time.Time

	actors actor.Executor
	idx    ports.FTSIndex
	w      *watcher

	cache *noteutil.NoteCache
	graph *noteutil.BacklinkGraph
}

// New creates a LocalVault rooted at root with the given scope.
func New(scope, root string, opts ...Option) (*LocalVault, error) {
	cfg := vaultConfig{
		clock:            time.Now,
		conflictPatterns: DefaultConflictPatterns,
		rescanInterval:   defaultRescanInterval,
		renamePairWindow: defaultRenamePairWindow,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create vault root: %w", err)
	}
	caseSensitive := probeCaseSensitivity(root)
	fts := cfg.ftsIndex
	if fts == nil {
		fts = index.New(cfg.indexOpts...)
	}
	exec := cfg.executor
	if exec == nil {
		exec = actor.New(cfg.actorOpts...)
	}
	log := cfg.log
	if log == nil {
		log = slog.Default()
	}
	v := &LocalVault{
		scope:  scope,
		root:   root,
		clock:  cfg.clock,
		actors: exec,
		idx:    fts,
		cache:  noteutil.NewNoteCache(),
		graph:  noteutil.NewBacklinkGraph(),
		caps: domain.Capabilities{
			Writable:      true,
			SoftDelete:    true,
			CaseSensitive: caseSensitive,
		},
	}
	if err := v.scanVault(); err != nil {
		return nil, fmt.Errorf("scan vault: %w", err)
	}
	v.w = newWatcher(v, log, root, cfg.conflictPatterns, cfg.rescanInterval, cfg.renamePairWindow)
	v.w.start()
	return v, nil
}

// Close shuts down background goroutines.
func (v *LocalVault) Close() error {
	v.w.close()
	v.actors.Close()
	v.idx.Close()
	return nil
}

func (v *LocalVault) Scope() domain.ScopeID             { return v.scope }
func (v *LocalVault) Capabilities() domain.Capabilities { return v.caps }

// IndexFile parses and indexes the note at absPath. No-op for non-markdown files.
func (v *LocalVault) IndexFile(absPath string) {
	if !noteutil.IsMDFile(absPath) {
		return
	}
	rel, err := filepath.Rel(v.root, absPath)
	if err != nil {
		return
	}
	np, err := v.normalizePath(filepath.ToSlash(rel))
	if err != nil {
		return
	}
	// Run through actor to serialize with concurrent mutations (e.g., Delete).
	// Without this, indexNote can race with Delete and recreate a deleted file
	// via os.WriteFile when writing back the derived NoteID.
	_ = v.actors.Do(context.Background(), v.scope, np.Storage, func() error {
		v.indexNote(absPath, np)
		return nil
	})
}

// RemoveFile removes the note at absPath from all indexes. No-op for non-markdown files.
func (v *LocalVault) RemoveFile(absPath string) {
	if !noteutil.IsMDFile(absPath) {
		return
	}
	rel, err := filepath.Rel(v.root, absPath)
	if err != nil {
		return
	}
	np, err := v.normalizePath(filepath.ToSlash(rel))
	if err != nil {
		return
	}
	v.removeNoteFromAllIndexes(np.IndexKey, np.Storage)
}

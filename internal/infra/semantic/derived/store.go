// Package derived implements DerivedMetadata storage in two modes:
// sidecar (server metadata DB) and frontmatter (YAML block in markdown file).
package derived

import (
	"context"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

// Mode selects the storage backend for DerivedMetadata.
type Mode int

const (
	ModeSidecar     Mode = iota // PostgreSQL metadata table; never touches markdown
	ModeFrontmatter             // derived: YAML block embedded in the markdown file
)

// Store is the port for reading and writing DerivedMetadata.
type Store interface {
	Get(ctx context.Context, noteID string) (*domain.DerivedMetadata, error)
	GetByRef(ctx context.Context, ref domain.NoteRef) (*domain.DerivedMetadata, error)
	Set(ctx context.Context, noteID string, ref domain.NoteRef, meta *domain.DerivedMetadata) error
	IsEditedByUser(ctx context.Context, noteID string) (bool, error)
}

// ConditionalWriteProber reports whether the underlying storage backend supports
// conditional (atomic) writes. Frontmatter mode requires this guarantee.
type ConditionalWriteProber interface {
	ProbeConditionalWrite(ctx context.Context) bool
}

// staticProber is a test helper that always returns a fixed result.
type staticProber bool

func (s staticProber) ProbeConditionalWrite(_ context.Context) bool { return bool(s) }

// StaticProber returns a ConditionalWriteProber that always reports ok.
func StaticProber(ok bool) ConditionalWriteProber { return staticProber(ok) }

// SelectMode returns ModeFrontmatter when the probe succeeds, ModeSidecar otherwise.
func SelectMode(prober ConditionalWriteProber) Mode {
	if prober.ProbeConditionalWrite(context.Background()) {
		return ModeFrontmatter
	}
	return ModeSidecar
}

// ShouldRederive returns true when meta's SchemaVersion is older than currentSchema.
// A newer SchemaVersion is treated as forward-compatible and preserved as-is.
func ShouldRederive(meta *domain.DerivedMetadata, currentSchema int) bool {
	return meta.SchemaVersion < currentSchema
}

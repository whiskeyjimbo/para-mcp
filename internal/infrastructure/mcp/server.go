// Package mcp wires NoteService to the MCP tool surface via stdio transport.
package mcp

import (
	"context"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
)

// ScopesFunc resolves the permitted scopes for a request.
type ScopesFunc func(ctx context.Context) []domain.ScopeID

// Option configures a Build call.
type Option func(*buildConfig)

type buildConfig struct {
	scopesFn      ScopesFunc
	serverName    string
	serverVersion string
	clock         func() time.Time
}

// WithScopesFunc sets the scope resolver (default: personal only).
func WithScopesFunc(fn ScopesFunc) Option {
	return func(c *buildConfig) { c.scopesFn = fn }
}

// WithServerName overrides the MCP server name (default: "paras").
func WithServerName(name string) Option {
	return func(c *buildConfig) { c.serverName = name }
}

// WithServerVersion overrides the MCP server version (default: "0.1.0").
func WithServerVersion(v string) Option {
	return func(c *buildConfig) { c.serverVersion = v }
}

// WithClock overrides the time source used by the stale notes handler (default: time.Now).
func WithClock(fn func() time.Time) Option {
	return func(c *buildConfig) { c.clock = fn }
}

func personalOnly(_ context.Context) []domain.ScopeID {
	return []domain.ScopeID{"personal"}
}

// Build constructs and returns an MCPServer wired to svc.
func Build(svc ports.NoteService, opts ...Option) *mcpserver.MCPServer {
	cfg := buildConfig{
		scopesFn:      personalOnly,
		serverName:    "paras",
		serverVersion: "0.1.0",
		clock:         time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}

	s := mcpserver.NewMCPServer(
		cfg.serverName,
		cfg.serverVersion,
		mcpserver.WithToolCapabilities(true),
	)

	h := &handlers{svc: svc, scopes: cfg.scopesFn, clock: cfg.clock}

	s.AddTool(toolNoteGet(), h.noteGet)
	s.AddTool(toolNoteCreate(), h.noteCreate)
	s.AddTool(toolNoteUpdateBody(), h.noteUpdateBody)
	s.AddTool(toolNotePatchFrontMatter(), h.notePatchFrontMatter)
	s.AddTool(toolNoteMove(), h.noteMove)
	s.AddTool(toolNoteArchive(), h.noteArchive)
	s.AddTool(toolNoteDelete(), h.noteDelete)
	s.AddTool(toolNotesList(), h.notesList)
	s.AddTool(toolNotesSearch(), h.notesSearch)
	s.AddTool(toolVaultStats(), h.vaultStats)

	s.AddTool(toolNotesBacklinks(), h.notesBacklinks)
	s.AddTool(toolNotesRelated(), h.notesRelated)
	s.AddTool(toolNotesStale(), h.notesStale)
	s.AddTool(toolVaultHealth(), h.vaultHealth)
	s.AddTool(toolVaultRescan(), h.vaultRescan)
	s.AddTool(toolVaultListScopes(), h.vaultListScopes)
	s.AddTool(toolNotesCreateBatch(), h.notesCreateBatch)
	s.AddTool(toolNotesUpdateBatch(), h.notesUpdateBatch)
	s.AddTool(toolNotesPatchFrontMatterBatch(), h.notesPatchFrontMatterBatch)

	return s
}

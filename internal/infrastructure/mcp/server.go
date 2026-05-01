// Package mcp wires NoteService to the MCP tool surface via stdio transport.
package mcp

import (
	"context"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"github.com/whiskeyjimbo/paras/internal/server/audit"
	"github.com/whiskeyjimbo/paras/internal/server/rbac"
)

// Option configures a Build call.
type Option func(*buildConfig)

type buildConfig struct {
	scopes           ports.ScopeResolver
	serverName       string
	serverVersion    string
	events           *EventBus
	auditSearcher    audit.Searcher
	rbacRegistry     *rbac.Registry
	exposeAdminTools bool
}

// WithScopeResolver sets the scope resolver (default: personal only).
func WithScopeResolver(r ports.ScopeResolver) Option {
	return func(c *buildConfig) { c.scopes = r }
}

// WithScopesFunc is a convenience wrapper that adapts a function to ScopeResolver.
func WithScopesFunc(fn func(context.Context) []domain.ScopeID) Option {
	return WithScopeResolver(ports.ScopesFunc(fn))
}

// WithServerName overrides the MCP server name (default: "paras").
func WithServerName(name string) Option {
	return func(c *buildConfig) { c.serverName = name }
}

// WithServerVersion overrides the MCP server version (default: "0.1.0").
func WithServerVersion(v string) Option {
	return func(c *buildConfig) { c.serverVersion = v }
}

// WithEventBus attaches an EventBus so mutation handlers publish note-change events.
func WithEventBus(bus *EventBus) Option {
	return func(c *buildConfig) { c.events = bus }
}

// WithAuditSearcher enables the audit_search admin tool backed by s.
func WithAuditSearcher(s audit.Searcher) Option {
	return func(c *buildConfig) { c.auditSearcher = s }
}

// WithRBACRegistry sets the RBAC registry used to gate admin tools.
func WithRBACRegistry(r *rbac.Registry) Option {
	return func(c *buildConfig) { c.rbacRegistry = r }
}

// WithExposeAdminTools enables admin tools (audit_search etc.) when true.
// Has no effect unless WithAuditSearcher and WithRBACRegistry are also set.
func WithExposeAdminTools(v bool) Option {
	return func(c *buildConfig) { c.exposeAdminTools = v }
}

var personalOnly ports.ScopeResolver = ports.ScopesFunc(func(_ context.Context) []domain.ScopeID {
	return []domain.ScopeID{"personal"}
})

// Build constructs and returns an MCPServer wired to svc.
func Build(svc ports.NoteService, opts ...Option) *mcpserver.MCPServer {
	cfg := buildConfig{
		scopes:        personalOnly,
		serverName:    "paras",
		serverVersion: "0.1.0",
	}
	for _, o := range opts {
		o(&cfg)
	}

	s := mcpserver.NewMCPServer(
		cfg.serverName,
		cfg.serverVersion,
		mcpserver.WithToolCapabilities(true),
	)

	h := &handlers{
		svc:              svc,
		scopes:           cfg.scopes,
		events:           cfg.events,
		auditSearcher:    cfg.auditSearcher,
		rbacRegistry:     cfg.rbacRegistry,
		exposeAdminTools: cfg.exposeAdminTools,
	}

	s.AddTool(toolNoteGet(), h.noteGet)
	s.AddTool(toolNoteCreate(), h.noteCreate)
	s.AddTool(toolNoteUpdateBody(), h.noteUpdateBody)
	s.AddTool(toolNotePatchFrontMatter(), h.notePatchFrontMatter)
	s.AddTool(toolNoteMove(), h.noteMove)
	s.AddTool(toolNoteArchive(), h.noteArchive)
	s.AddTool(toolNoteDelete(), h.noteDelete)
	s.AddTool(toolNotePromote(), h.notePromote)
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

	if cfg.exposeAdminTools {
		s.AddTool(toolAuditSearch(), h.auditSearch)
	}

	return s
}

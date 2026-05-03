// Package rbac resolves caller roles per scope and produces AllowedScopes lists.
package rbac

import (
	"errors"
	"sync"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/server/auth"
)

// Role represents the access level a caller holds on a scope.
type Role int

const (
	Viewer      Role = iota // read only
	Contributor             // read + write
	Lead                    // + promote
	Admin                   // + admin tools
)

// ScopeGrant maps an identity to its role on a single scope.
type ScopeGrant struct {
	Identity auth.CallerIdentity
	Scope    domain.ScopeID
	Role     Role
}

// Registry holds role assignments and resolves AllowedScopes for a caller.
type Registry struct {
	mu     sync.RWMutex
	grants map[auth.CallerIdentity]map[domain.ScopeID]Role
}

// RegistryOption configures a Registry.
type RegistryOption func(*Registry)

// WithRoleLoader populates the registry with a static set of grants.
func WithRoleLoader(grants []ScopeGrant) RegistryOption {
	return func(r *Registry) {
		for _, g := range grants {
			r.set(g.Identity, g.Scope, g.Role)
		}
	}
}

// New constructs a Registry with the given options applied.
func New(opts ...RegistryOption) *Registry {
	r := &Registry{grants: make(map[auth.CallerIdentity]map[domain.ScopeID]Role)}
	for _, o := range opts {
		o(r)
	}
	return r
}

func (r *Registry) set(id auth.CallerIdentity, scope domain.ScopeID, role Role) {
	if r.grants[id] == nil {
		r.grants[id] = make(map[domain.ScopeID]Role)
	}
	r.grants[id][scope] = role
}

// Reload replaces all grants atomically. Safe for concurrent use.
func (r *Registry) Reload(grants []ScopeGrant) {
	next := make(map[auth.CallerIdentity]map[domain.ScopeID]Role)
	for _, g := range grants {
		if next[g.Identity] == nil {
			next[g.Identity] = make(map[domain.ScopeID]Role)
		}
		next[g.Identity][g.Scope] = g.Role
	}
	r.mu.Lock()
	r.grants = next
	r.mu.Unlock()
}

// AllowedScopes resolves which of the requested scopes caller may access.
// Empty requested means "caller's full visibility chain" — returns all scopes
// where caller holds at least Viewer.
// Returns an error (never nil slice) on programmer error.
// Returns []domain.ScopeID{} as a valid "deny everything" result.
func (r *Registry) AllowedScopes(caller auth.CallerIdentity, requested []domain.ScopeID) ([]domain.ScopeID, error) {
	r.mu.RLock()
	callerGrants := r.grants[caller]
	r.mu.RUnlock()

	if len(requested) == 0 {
		// Return caller's full visibility chain.
		out := make([]domain.ScopeID, 0, len(callerGrants))
		for scope := range callerGrants {
			out = append(out, scope)
		}
		return out, nil
	}

	out := make([]domain.ScopeID, 0, len(requested))
	for _, s := range requested {
		if _, ok := callerGrants[s]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// HasRole reports whether caller holds at least the given role on scope.
func (r *Registry) HasRole(caller auth.CallerIdentity, scope domain.ScopeID, minimum Role) bool {
	r.mu.RLock()
	role, ok := r.grants[caller][scope]
	r.mu.RUnlock()
	return ok && role >= minimum
}

// ErrAllowedScopesNil is returned when AllowedScopes would be nil due to a
// programmer error (e.g. nil registry passed to application layer).
var ErrAllowedScopesNil = errors.New("internal: AllowedScopes resolver is nil")

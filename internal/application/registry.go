package application

import (
	"fmt"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
)

// VaultEntry holds a registered vault and its per-vault service wrapper.
type VaultEntry struct {
	ScopeID         domain.ScopeID
	CanonicalRemote string // empty for local vaults
	vault           ports.Vault
	svc             *NoteService
}

// VaultRegistry routes vault operations to the correct Vault by scope.
// The first registered vault is the primary (local) vault used for
// operations that do not carry a scope.
type VaultRegistry struct {
	entries []VaultEntry
	byScope map[domain.ScopeID]*VaultEntry
}

// NewRegistry creates an empty registry.
func NewRegistry() *VaultRegistry {
	return &VaultRegistry{byScope: make(map[domain.ScopeID]*VaultEntry)}
}

// AddVault registers a vault. canonicalRemote is empty for local vaults.
// The scope "personal" is reserved and may not be used as a remote alias.
func (r *VaultRegistry) AddVault(vault ports.Vault, canonicalRemote string, opts ...Option) error {
	scope := vault.Scope()
	if _, exists := r.byScope[scope]; exists {
		return fmt.Errorf("scope %q already registered", scope)
	}
	r.entries = append(r.entries, VaultEntry{
		ScopeID:         scope,
		CanonicalRemote: canonicalRemote,
		vault:           vault,
		svc:             NewService(vault, opts...),
	})
	r.byScope[scope] = &r.entries[len(r.entries)-1]
	return nil
}

// EntryFor returns the vault entry for the given scope.
func (r *VaultRegistry) EntryFor(scope domain.ScopeID) (*VaultEntry, bool) {
	e, ok := r.byScope[scope]
	return e, ok
}

// Entries returns all registered vault entries in registration order.
func (r *VaultRegistry) Entries() []VaultEntry { return r.entries }

// Close closes all vaults in registration order.
func (r *VaultRegistry) Close() error {
	var first error
	for _, e := range r.entries {
		if err := e.vault.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

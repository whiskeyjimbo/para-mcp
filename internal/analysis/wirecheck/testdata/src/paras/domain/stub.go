package domain

type ScopeID string

type AuthFilter struct {
	AllowedScopes []ScopeID
}

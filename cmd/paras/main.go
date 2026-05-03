package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/whiskeyjimbo/para-mcp/internal/application"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
	"github.com/whiskeyjimbo/para-mcp/internal/infra/remotevault"
	mcplayer "github.com/whiskeyjimbo/para-mcp/internal/infrastructure/mcp"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/localvault"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/tombstone"
	"github.com/whiskeyjimbo/para-mcp/internal/server/auth"
	"gopkg.in/yaml.v3"
)

type config struct {
	Local   localConfig    `yaml:"local"`
	Remotes []remoteConfig `yaml:"remotes"`
	Server  serverConfig   `yaml:"server"`
}

type serverConfig struct {
	Auth authConfig `yaml:"auth"`
}

// authConfig holds server-mode authentication settings.
// Mode selects the auth mechanism: "bearer", "oidc", or "" (no auth — dev only).
// BearerTokens maps raw token strings to CallerIdentity names (used when Mode="bearer").
// JWKSEndpoint is the OIDC provider's JWKS URL (used when Mode="oidc").
type authConfig struct {
	Mode         string            `yaml:"mode"`
	BearerTokens map[string]string `yaml:"bearer_tokens"`
	JWKSEndpoint string            `yaml:"jwks_endpoint"`
}

type localConfig struct {
	Vault          string `yaml:"vault"`
	Scope          string `yaml:"scope"`
	TombstoneStore string `yaml:"tombstone_store"`
}

type remoteConfig struct {
	Scope           string `yaml:"scope"`
	CanonicalRemote string `yaml:"canonical_remote"`
	URL             string `yaml:"url"`
}

func main() {
	vaultRoot := flag.String("vault", "", "path to PARA vault root (single-vault mode)")
	scopeID := flag.String("scope", "personal", "scope identifier for this vault (single-vault mode)")
	configFile := flag.String("config", "", "path to federation config file (YAML)")
	addr := flag.String("addr", "", "HTTP listen address for server mode (e.g. :8080)")
	requirePromotionApproval := flag.Bool("require-promotion-approval", false, "gate note_promote with pending_approval (ADR-0006)")
	authMode := flag.String("auth-mode", "", "auth mechanism for server mode: bearer or oidc (empty = no auth)")
	jwksEndpoint := flag.String("jwks-endpoint", "", "OIDC JWKS endpoint URL (required when --auth-mode=oidc)")
	bearerTokensFile := flag.String("bearer-tokens-file", "", "JSON file mapping bearer token strings to caller identities (required when --auth-mode=bearer)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var (
		svc    ports.NoteService
		scopes []domain.ScopeID
		cfg    config
	)

	if *configFile != "" {
		cfg = mustParseConfig(*configFile)
		svc, scopes = mustBuildFederatedFromConfig(ctx, cfg)
	} else {
		if *vaultRoot == "" {
			fmt.Fprintln(os.Stderr, "error: --vault or --config is required")
			flag.Usage()
			os.Exit(1)
		}
		scope, scopeErr := domain.NormalizeScopeID(*scopeID)
		if scopeErr != nil {
			fmt.Fprintf(os.Stderr, "error: invalid --scope %q: %v\n", *scopeID, scopeErr)
			os.Exit(1)
		}
		v, err := localvault.New(scope, *vaultRoot)
		if err != nil {
			slog.Error("failed to open vault", "err", err)
			os.Exit(1)
		}
		defer v.Close()
		svc = application.NewService(v)
		scopes = []domain.ScopeID{scope}
	}

	bus := mcplayer.NewEventBus()
	s := mcplayer.Build(svc,
		mcplayer.WithScopesFunc(func(_ context.Context) []domain.ScopeID { return scopes }),
		mcplayer.WithEventBus(bus),
		mcplayer.WithRequirePromotionApproval(*requirePromotionApproval),
	)

	if *addr != "" {
		// Resolve auth config: flags take precedence over YAML server.auth block.
		ac := cfg.Server.Auth
		if *authMode != "" {
			ac.Mode = *authMode
		}
		if *jwksEndpoint != "" {
			ac.JWKSEndpoint = *jwksEndpoint
		}
		if *bearerTokensFile != "" {
			tokens, err := loadBearerTokensFile(*bearerTokensFile)
			if err != nil {
				slog.Error("failed to load bearer tokens file", "path", *bearerTokensFile, "err", err)
				os.Exit(1)
			}
			ac.BearerTokens = tokens
		}

		authMW, err := buildAuthMiddleware(ac)
		if err != nil {
			slog.Error("invalid auth configuration", "err", err)
			os.Exit(1)
		}

		mcpSrv := mcpserver.NewStreamableHTTPServer(s, mcpserver.WithStateLess(true))
		mux := http.NewServeMux()
		mux.Handle("/", authMW(mcplayer.ScopeMemoMiddleware(mcplayer.RequestIDMiddleware(mcpSrv))))
		mux.Handle("/events", mcplayer.SSEHandler(bus))
		httpSrv := &http.Server{
			Addr:    *addr,
			Handler: mux,
		}
		slog.Info("starting HTTP server", "addr", *addr, "auth", ac.Mode)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "err", err)
			os.Exit(1)
		}
		return
	}

	stdio := mcpserver.NewStdioServer(s)
	if err := stdio.Listen(ctx, os.Stdin, os.Stdout); err != nil {
		slog.Error("stdio server error", "err", err)
		os.Exit(1)
	}
}

func mustParseConfig(cfgPath string) config {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		slog.Error("failed to read config", "path", cfgPath, "err", err)
		os.Exit(1)
	}
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		slog.Error("failed to parse config", "path", cfgPath, "err", err)
		os.Exit(1)
	}
	return cfg
}

// mustBuildFederatedFromConfig wires the VaultRegistry and FederationService from a parsed config,
// and returns the service plus the full list of registered scope IDs (for the scope resolver).
func mustBuildFederatedFromConfig(ctx context.Context, cfg config) (ports.NoteService, []domain.ScopeID) {
	if cfg.Local.Vault == "" {
		slog.Error("config missing local.vault")
		os.Exit(1)
	}

	localScope := cfg.Local.Scope
	if localScope == "" {
		localScope = "personal"
	}
	scope, err := domain.NormalizeScopeID(localScope)
	if err != nil {
		slog.Error("invalid local scope", "scope", localScope, "err", err)
		os.Exit(1)
	}

	lv, err := localvault.New(scope, cfg.Local.Vault)
	if err != nil {
		slog.Error("failed to open local vault", "err", err)
		os.Exit(1)
	}

	reg := application.NewRegistry()
	if err := reg.AddVault(lv, ""); err != nil {
		slog.Error("failed to register local vault", "err", err)
		os.Exit(1)
	}

	for _, rc := range cfg.Remotes {
		remoteScope, err := domain.NormalizeScopeID(rc.Scope)
		if err != nil {
			slog.Error("invalid remote scope", "scope", rc.Scope, "err", err)
			os.Exit(1)
		}
		rv, err := remotevault.New(ctx, remotevault.Config{
			LocalScope:      remoteScope,
			CanonicalRemote: rc.CanonicalRemote,
			BaseURL:         rc.URL,
		})
		if err != nil {
			slog.Error("failed to connect to remote vault", "scope", rc.Scope, "url", rc.URL, "err", err)
			os.Exit(1)
		}
		if err := reg.AddVault(rv, rc.CanonicalRemote); err != nil {
			slog.Error("failed to register remote vault", "scope", rc.Scope, "err", err)
			os.Exit(1)
		}
		if rv.Capabilities().Watch {
			rv.StartWatch(ctx)
			slog.Info("started SSE watch for remote vault", "scope", remoteScope)
		}
		slog.Info("registered remote vault", "scope", remoteScope, "url", rc.URL)
	}

	var fedOpts []application.FederationOption
	if cfg.Local.TombstoneStore != "" {
		ts, err := tombstone.New(cfg.Local.TombstoneStore)
		if err != nil {
			slog.Error("failed to open tombstone store", "path", cfg.Local.TombstoneStore, "err", err)
			os.Exit(1)
		}
		fedOpts = append(fedOpts, application.WithTombstoneStore(ts))
		slog.Info("tombstone store loaded", "path", cfg.Local.TombstoneStore)
	}

	fed, err := application.NewFederationService(reg, fedOpts...)
	if err != nil {
		slog.Error("failed to create federation service", "err", err)
		os.Exit(1)
	}

	allScopes := make([]domain.ScopeID, 0, len(reg.Entries()))
	for _, e := range reg.Entries() {
		allScopes = append(allScopes, e.ScopeID)
	}
	return fed, allScopes
}

// buildAuthMiddleware returns an HTTP middleware based on ac.Mode.
// Mode "bearer" requires BearerTokens to be populated.
// Mode "oidc" requires JWKSEndpoint to be set.
// Empty mode returns an identity middleware (no auth — dev/stdio only).
func buildAuthMiddleware(ac authConfig) (func(http.Handler) http.Handler, error) {
	switch ac.Mode {
	case "bearer":
		if len(ac.BearerTokens) == 0 {
			return nil, fmt.Errorf("auth-mode=bearer requires bearer_tokens to be configured")
		}
		store := make(auth.MapTokenStore, len(ac.BearerTokens))
		for token, identity := range ac.BearerTokens {
			store[token] = auth.CallerIdentity(identity)
		}
		return auth.BearerMiddleware(auth.WithTokenStore(store)), nil
	case "oidc":
		if ac.JWKSEndpoint == "" {
			return nil, fmt.Errorf("auth-mode=oidc requires jwks_endpoint to be configured")
		}
		return auth.OIDCMiddleware(auth.WithJWKSEndpoint(ac.JWKSEndpoint)), nil
	case "":
		slog.Warn("no auth mode configured for HTTP server; all requests will be accepted")
		return func(next http.Handler) http.Handler { return next }, nil
	default:
		return nil, fmt.Errorf("unknown auth-mode %q; valid values: bearer, oidc", ac.Mode)
	}
}

// loadBearerTokensFile reads a JSON file mapping bearer token strings to caller identity names.
func loadBearerTokensFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tokens map[string]string
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return tokens, nil
}

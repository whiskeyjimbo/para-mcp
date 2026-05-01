package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/whiskeyjimbo/paras/internal/application"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"github.com/whiskeyjimbo/paras/internal/infra/remotevault"
	mcplayer "github.com/whiskeyjimbo/paras/internal/infrastructure/mcp"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/localvault"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/tombstone"
	"gopkg.in/yaml.v3"
)

type config struct {
	Local   localConfig    `yaml:"local"`
	Remotes []remoteConfig `yaml:"remotes"`
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
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var (
		svc    ports.NoteService
		scopes []domain.ScopeID
	)

	if *configFile != "" {
		svc, scopes = mustBuildFederated(ctx, *configFile)
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
		mcpSrv := mcpserver.NewStreamableHTTPServer(s, mcpserver.WithStateLess(true))
		mux := http.NewServeMux()
		mux.Handle("/", mcplayer.ScopeMemoMiddleware(mcplayer.RequestIDMiddleware(mcpSrv)))
		mux.Handle("/events", mcplayer.SSEHandler(bus))
		httpSrv := &http.Server{
			Addr:    *addr,
			Handler: mux,
		}
		slog.Info("starting HTTP server", "addr", *addr)
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

// mustBuildFederated loads the config, wires the VaultRegistry and FederationService,
// and returns the service plus the full list of registered scope IDs (for the scope resolver).
func mustBuildFederated(ctx context.Context, cfgPath string) (ports.NoteService, []domain.ScopeID) {
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

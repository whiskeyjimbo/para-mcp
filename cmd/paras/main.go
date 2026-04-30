package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/whiskeyjimbo/paras/internal/application"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	mcplayer "github.com/whiskeyjimbo/paras/internal/infrastructure/mcp"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/localvault"
)

func main() {
	vaultRoot := flag.String("vault", "", "path to PARA vault root (required)")
	scopeID := flag.String("scope", "personal", "scope identifier for this vault")
	flag.Parse()

	if *vaultRoot == "" {
		fmt.Fprintln(os.Stderr, "error: --vault is required")
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

	svc := application.NewService(v)
	s := mcplayer.Build(svc)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	stdio := mcpserver.NewStdioServer(s)
	if err := stdio.Listen(ctx, os.Stdin, os.Stdout); err != nil {
		slog.Error("stdio server error", "err", err)
		os.Exit(1)
	}
}

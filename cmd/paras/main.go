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
	"github.com/whiskeyjimbo/paras/internal/index"
	mcplayer "github.com/whiskeyjimbo/paras/internal/mcp"
	"github.com/whiskeyjimbo/paras/internal/vault"
)

func main() {
	vaultRoot := flag.String("vault", "", "path to PARA vault root (required)")
	flag.Parse()

	if *vaultRoot == "" {
		fmt.Fprintln(os.Stderr, "error: --vault is required")
		flag.Usage()
		os.Exit(1)
	}

	v, err := vault.New("personal", *vaultRoot, index.Config{})
	if err != nil {
		slog.Error("failed to open vault", "err", err)
		os.Exit(1)
	}
	defer v.Close()

	svc := vault.NewService(v)
	s := mcplayer.Build(svc, v)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	stdio := mcpserver.NewStdioServer(s)
	if err := stdio.Listen(ctx, os.Stdin, os.Stdout); err != nil {
		slog.Error("stdio server error", "err", err)
		os.Exit(1)
	}
}

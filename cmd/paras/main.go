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
	mcplayer "github.com/whiskeyjimbo/paras/internal/infrastructure/mcp"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/localvault"
)

func main() {
	vaultRoot := flag.String("vault", "", "path to PARA vault root (required)")
	flag.Parse()

	if *vaultRoot == "" {
		fmt.Fprintln(os.Stderr, "error: --vault is required")
		flag.Usage()
		os.Exit(1)
	}

	v, err := localvault.New("personal", *vaultRoot)
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

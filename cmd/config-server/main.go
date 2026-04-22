package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aap/config-server/internal/config"
	"github.com/aap/config-server/internal/gitops"
	"github.com/aap/config-server/internal/handler"
	"github.com/aap/config-server/internal/server"
	"github.com/aap/config-server/internal/store"
)

func main() {
	cfg := config.Load()
	setupLogger(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	repo, err := gitops.New(gitops.Options{
		LocalPath:  cfg.GitLocalPath,
		RemoteURL:  cfg.GitURL,
		Branch:     cfg.GitBranch,
		SSHKeyPath: cfg.GitSSHKeyPath,
	})
	if err != nil {
		slog.Error("create git repo", "err", err)
		os.Exit(1)
	}

	st := store.New(repo)
	if err := st.LoadFromRepo(ctx); err != nil {
		slog.Error("initial config load", "err", err)
		os.Exit(1)
	}

	// srv is the readiness provider; handler holds a pointer to it so the
	// circular dependency is resolved at construction time without a factory.
	srv := server.New(cfg.Addr, nil) // handler set below
	h := handler.New(st, srv)

	mux := http.NewServeMux()
	h.Routes(mux)
	srv.SetHandler(mux)
	srv.MarkReady()

	go pollLoop(ctx, st, cfg.GitPollInterval)

	if err := srv.Run(ctx); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

func pollLoop(ctx context.Context, st *store.Store, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := st.RefreshFromRepo(ctx); err != nil {
				slog.Warn("git poll failed", "err", err)
			}
		}
	}
}

func setupLogger(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})))
}

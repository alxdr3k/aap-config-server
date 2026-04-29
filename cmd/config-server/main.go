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
	"github.com/aap/config-server/internal/secret"
	"github.com/aap/config-server/internal/server"
	"github.com/aap/config-server/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Logger hasn't been configured yet; write to stderr directly.
		_, _ = os.Stderr.WriteString("config: " + err.Error() + "\n")
		os.Exit(2)
	}
	setupLogger(cfg.LogLevel)

	if cfg.APIKey == "" && cfg.AllowUnauthenticatedDev {
		slog.Warn("admin API authentication is DISABLED (ALLOW_UNAUTHENTICATED_DEV=true). Never use this in production.")
	}
	secretCfg := secret.RuntimeConfig{
		MountPath:                       cfg.SecretMountPath,
		SealedSecretControllerNamespace: cfg.SealedSecretControllerNamespace,
		SealedSecretControllerName:      cfg.SealedSecretControllerName,
		SealedSecretScope:               cfg.SealedSecretScope,
		K8sApplyTimeout:                 cfg.K8sApplyTimeout,
		AuditLogEnabled:                 cfg.SecretAuditLogEnabled,
	}
	slog.Info("secret runtime boundary configured",
		"mount_path", secretCfg.MountPath,
		"sealed_secret_controller_namespace", secretCfg.SealedSecretControllerNamespace,
		"sealed_secret_controller_name", secretCfg.SealedSecretControllerName,
		"sealed_secret_scope", secretCfg.SealedSecretScope,
		"k8s_apply_timeout", secretCfg.K8sApplyTimeout,
		"audit_log_enabled", secretCfg.AuditLogEnabled)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	repo, err := gitops.New(gitops.Options{
		LocalPath:  cfg.GitLocalPath,
		RemoteURL:  cfg.GitURL,
		Branch:     cfg.GitBranch,
		SSHKeyPath: cfg.GitSSHKeyPath,
		Username:   cfg.GitUsername,
		Password:   cfg.GitPassword,
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

	probe := &server.ReadinessProbe{}
	h := handler.New(st, probe, cfg.APIKey)

	mux := http.NewServeMux()
	h.Routes(mux)

	srv := server.New(cfg.Addr, mux)
	probe.MarkReady()

	go pollLoop(ctx, st, cfg.GitPollInterval)

	if err := srv.Run(ctx); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

func pollLoop(ctx context.Context, st *store.Store, interval time.Duration) {
	if interval <= 0 {
		slog.Warn("git poll disabled: interval <= 0", "interval", interval)
		return
	}
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

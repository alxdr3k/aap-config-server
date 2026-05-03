package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/aap/config-server/internal/agent"
)

func main() {
	cfg, err := agent.LoadConfig(os.Args[1:], os.Getenv)
	if err != nil {
		_, _ = os.Stderr.WriteString("config-agent: " + err.Error() + "\n")
		os.Exit(2)
	}
	setupLogger(cfg.LogLevel)

	client, err := agent.NewClient(agent.ClientOptions{
		BaseURL: cfg.ConfigServerURL,
		APIKey:  cfg.APIKey,
		HTTPClient: &http.Client{
			Timeout: cfg.HTTPTimeout,
		},
	})
	if err != nil {
		slog.Error("create config server client", "err", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	result, err := agent.RunDryRun(ctx, cfg, client)
	if err != nil {
		slog.Error("config-agent dry run failed", "err", err)
		os.Exit(1)
	}
	slog.Info("config-agent dry run complete",
		"org", result.Ref.Org,
		"project", result.Ref.Project,
		"service", result.Ref.Service,
		"version", result.Version,
		"config_keys", result.ConfigKeys,
		"plain_env_vars", result.PlainEnvVars,
		"secret_refs", result.SecretRefs,
		"resolved_secrets", result.ResolvedSecrets,
		"resolve_secrets", result.ResolveSecrets)
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

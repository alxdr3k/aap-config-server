package config

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// ServerConfig holds all runtime configuration for the Config Server.
// Values are sourced from environment variables (primary) with flag fallbacks.
// Helm values → K8s env vars → this struct.
type ServerConfig struct {
	Addr                     string
	GitURL                   string
	GitBranch                string
	GitLocalPath             string
	GitPollInterval          time.Duration
	GitSSHKeyPath            string
	APIKey                   string
	AllowUnauthenticatedDev  bool
	SecretMountPath          string
	ConsoleAPIURL            string
	LogLevel                 string
}

// Load reads configuration from environment variables and command-line flags,
// validates the result, and returns it. Errors are reported via the error
// return rather than os.Exit so callers (including tests) can decide how to
// react.
func Load() (*ServerConfig, error) {
	cfg := &ServerConfig{}

	flag.StringVar(&cfg.Addr, "addr", env("ADDR", ":8080"), "HTTP listen address")
	flag.StringVar(&cfg.GitURL, "git-url", env("GIT_URL", ""), "Git repository URL (required)")
	flag.StringVar(&cfg.GitBranch, "git-branch", env("GIT_BRANCH", "main"), "Git branch")
	flag.StringVar(&cfg.GitLocalPath, "git-local-path", env("GIT_LOCAL_PATH", "/tmp/aap-helm-charts"), "Local git clone path")
	flag.DurationVar(&cfg.GitPollInterval, "git-poll-interval", envDuration("GIT_POLL_INTERVAL", 30*time.Second), "Git poll interval (must be > 0)")
	flag.StringVar(&cfg.GitSSHKeyPath, "git-ssh-key", env("GIT_SSH_KEY", ""), "Path to SSH private key for git auth")
	flag.StringVar(&cfg.SecretMountPath, "secret-mount-path", env("SECRET_MOUNT_PATH", "/secrets"), "Volume mount path for K8s secrets")
	flag.StringVar(&cfg.ConsoleAPIURL, "console-api-url", env("CONSOLE_API_URL", ""), "AAP Console API URL")
	flag.StringVar(&cfg.LogLevel, "log-level", env("LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")

	// API_KEY is env-only — never accept via flag (would expose via ps).
	cfg.APIKey = os.Getenv("API_KEY")
	cfg.AllowUnauthenticatedDev = envBool("ALLOW_UNAUTHENTICATED_DEV", false)

	flag.Parse()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate enforces required fields and sane values. Kept on the struct so
// tests can construct a ServerConfig directly and re-use the same rules.
func (c *ServerConfig) Validate() error {
	if c.GitURL == "" {
		return errors.New("GIT_URL is required (set via env or -git-url flag)")
	}
	if c.GitPollInterval <= 0 {
		return fmt.Errorf("GIT_POLL_INTERVAL must be > 0, got %s", c.GitPollInterval)
	}
	if c.APIKey == "" && !c.AllowUnauthenticatedDev {
		return errors.New("API_KEY is required. Set ALLOW_UNAUTHENTICATED_DEV=true only for local dev/test")
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("invalid duration env var, using fallback", "key", key, "value", v, "fallback", fallback)
		return fallback
	}
	return d
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	case "0", "false", "FALSE", "False", "no", "off":
		return false
	default:
		slog.Warn("invalid bool env var, using fallback", "key", key, "value", v, "fallback", fallback)
		return fallback
	}
}

package config

import (
	"flag"
	"log"
	"os"
	"time"
)

// ServerConfig holds all runtime configuration for the Config Server.
// Values are sourced from environment variables (primary) with flag fallbacks.
// Helm values → K8s env vars → this struct.
type ServerConfig struct {
	Addr            string
	GitURL          string
	GitBranch       string
	GitLocalPath    string
	GitPollInterval time.Duration
	GitSSHKeyPath   string
	APIKey          string
	SecretMountPath string
	ConsoleAPIURL   string
	LogLevel        string
}

// Load reads configuration from environment variables and command-line flags.
// Fatal on missing required fields.
func Load() *ServerConfig {
	cfg := &ServerConfig{}

	flag.StringVar(&cfg.Addr, "addr", env("ADDR", ":8080"), "HTTP listen address")
	flag.StringVar(&cfg.GitURL, "git-url", env("GIT_URL", ""), "Git repository URL (required)")
	flag.StringVar(&cfg.GitBranch, "git-branch", env("GIT_BRANCH", "main"), "Git branch")
	flag.StringVar(&cfg.GitLocalPath, "git-local-path", env("GIT_LOCAL_PATH", "/tmp/aap-helm-charts"), "Local git clone path")
	flag.DurationVar(&cfg.GitPollInterval, "git-poll-interval", envDuration("GIT_POLL_INTERVAL", 30*time.Second), "Git poll interval")
	flag.StringVar(&cfg.GitSSHKeyPath, "git-ssh-key", env("GIT_SSH_KEY", ""), "Path to SSH private key for git auth")
	flag.StringVar(&cfg.SecretMountPath, "secret-mount-path", env("SECRET_MOUNT_PATH", "/secrets"), "Volume mount path for K8s secrets")
	flag.StringVar(&cfg.ConsoleAPIURL, "console-api-url", env("CONSOLE_API_URL", ""), "AAP Console API URL")
	flag.StringVar(&cfg.LogLevel, "log-level", env("LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")

	// API_KEY is env-only — never accept via flag (would expose via ps)
	cfg.APIKey = os.Getenv("API_KEY")

	flag.Parse()

	if cfg.GitURL == "" {
		log.Fatal("GIT_URL is required (set via env or -git-url flag)")
	}

	return cfg
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return fallback
}

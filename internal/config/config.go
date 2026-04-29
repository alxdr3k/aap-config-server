package config

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const (
	defaultSecretMountPath                 = "/secrets"
	defaultSealedSecretControllerNamespace = "kube-system"
	defaultSealedSecretControllerName      = "sealed-secrets-controller"
	defaultSealedSecretScope               = "strict"
	defaultK8sApplyTimeout                 = 10 * time.Second
	defaultSecretAuditLogEnabled           = true
	sealedSecretScopeStrict                = "strict"
	sealedSecretScopeNamespaceWide         = "namespace-wide"
	sealedSecretScopeClusterWide           = "cluster-wide"
)

// ServerConfig holds all runtime configuration for the Config Server.
// Values are sourced from environment variables (primary) with flag fallbacks.
// Helm values → K8s env vars → this struct.
type ServerConfig struct {
	Addr                            string
	GitURL                          string
	GitBranch                       string
	GitLocalPath                    string
	GitPollInterval                 time.Duration
	GitSSHKeyPath                   string
	GitUsername                     string
	GitPassword                     string
	APIKey                          string
	AllowUnauthenticatedDev         bool
	SecretMountPath                 string
	SealedSecretControllerNamespace string
	SealedSecretControllerName      string
	SealedSecretScope               string
	K8sApplyTimeout                 time.Duration
	SecretAuditLogEnabled           bool
	ConsoleAPIURL                   string
	LogLevel                        string
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
	flag.StringVar(&cfg.GitUsername, "git-username", env("GIT_USERNAME", ""), "Username for HTTPS BasicAuth (use with GIT_PASSWORD)")
	flag.StringVar(&cfg.SecretMountPath, "secret-mount-path", env("SECRET_MOUNT_PATH", defaultSecretMountPath), "Volume mount path for K8s secrets")
	flag.StringVar(&cfg.SealedSecretControllerNamespace, "sealed-secret-controller-namespace", env("SEALED_SECRET_CONTROLLER_NAMESPACE", defaultSealedSecretControllerNamespace), "Namespace of the SealedSecret controller service")
	flag.StringVar(&cfg.SealedSecretControllerName, "sealed-secret-controller-name", env("SEALED_SECRET_CONTROLLER_NAME", defaultSealedSecretControllerName), "Name of the SealedSecret controller service")
	flag.StringVar(&cfg.SealedSecretScope, "sealed-secret-scope", env("SEALED_SECRET_SCOPE", defaultSealedSecretScope), "SealedSecret scope: strict, namespace-wide, or cluster-wide")
	flag.DurationVar(&cfg.K8sApplyTimeout, "k8s-apply-timeout", envDuration("K8S_APPLY_TIMEOUT", defaultK8sApplyTimeout), "Timeout for future Kubernetes apply calls")
	flag.BoolVar(&cfg.SecretAuditLogEnabled, "secret-audit-log-enabled", envBool("SECRET_AUDIT_LOG_ENABLED", defaultSecretAuditLogEnabled), "Enable audit logs for future secret reads/writes")
	flag.StringVar(&cfg.ConsoleAPIURL, "console-api-url", env("CONSOLE_API_URL", ""), "AAP Console API URL")
	flag.StringVar(&cfg.LogLevel, "log-level", env("LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")

	// API_KEY and GIT_PASSWORD are env-only — never accept via flag (would expose via ps).
	cfg.APIKey = os.Getenv("API_KEY")
	cfg.GitPassword = os.Getenv("GIT_PASSWORD")
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
	c.applyDefaults()

	if c.GitURL == "" {
		return errors.New("GIT_URL is required (set via env or -git-url flag)")
	}
	if c.GitPollInterval <= 0 {
		return fmt.Errorf("GIT_POLL_INTERVAL must be > 0, got %s", c.GitPollInterval)
	}
	if c.APIKey == "" && !c.AllowUnauthenticatedDev {
		return errors.New("API_KEY is required. Set ALLOW_UNAUTHENTICATED_DEV=true only for local dev/test")
	}
	if c.GitSSHKeyPath != "" && (c.GitUsername != "" || c.GitPassword != "") {
		return errors.New("set either GIT_SSH_KEY or GIT_USERNAME/GIT_PASSWORD, not both")
	}
	if (c.GitUsername != "") != (c.GitPassword != "") {
		return errors.New("GIT_USERNAME and GIT_PASSWORD must be set together")
	}
	if c.SecretMountPath == "" {
		return errors.New("SECRET_MOUNT_PATH is required")
	}
	if !filepath.IsAbs(c.SecretMountPath) {
		return fmt.Errorf("SECRET_MOUNT_PATH must be absolute, got %q", c.SecretMountPath)
	}
	if c.SealedSecretControllerNamespace == "" {
		return errors.New("SEALED_SECRET_CONTROLLER_NAMESPACE is required")
	}
	if c.SealedSecretControllerName == "" {
		return errors.New("SEALED_SECRET_CONTROLLER_NAME is required")
	}
	switch c.SealedSecretScope {
	case sealedSecretScopeStrict, sealedSecretScopeNamespaceWide, sealedSecretScopeClusterWide:
	default:
		return fmt.Errorf("SEALED_SECRET_SCOPE must be one of %q, %q, or %q, got %q",
			sealedSecretScopeStrict, sealedSecretScopeNamespaceWide, sealedSecretScopeClusterWide, c.SealedSecretScope)
	}
	if c.K8sApplyTimeout <= 0 {
		return fmt.Errorf("K8S_APPLY_TIMEOUT must be > 0, got %s", c.K8sApplyTimeout)
	}
	return nil
}

func (c *ServerConfig) applyDefaults() {
	secretRuntimeUnset := c.SecretMountPath == "" &&
		c.SealedSecretControllerNamespace == "" &&
		c.SealedSecretControllerName == "" &&
		c.SealedSecretScope == "" &&
		c.K8sApplyTimeout == 0

	if c.SecretMountPath == "" {
		c.SecretMountPath = defaultSecretMountPath
	}
	if c.SealedSecretControllerNamespace == "" {
		c.SealedSecretControllerNamespace = defaultSealedSecretControllerNamespace
	}
	if c.SealedSecretControllerName == "" {
		c.SealedSecretControllerName = defaultSealedSecretControllerName
	}
	if c.SealedSecretScope == "" {
		c.SealedSecretScope = defaultSealedSecretScope
	}
	if secretRuntimeUnset {
		c.K8sApplyTimeout = defaultK8sApplyTimeout
		c.SecretAuditLogEnabled = defaultSecretAuditLogEnabled
	}
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

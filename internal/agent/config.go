package agent

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout     = 5 * time.Second
	defaultPollInterval    = 30 * time.Second
	defaultDebounceCool    = 10 * time.Second
	defaultDebounceQuiet   = 10 * time.Second
	defaultDebounceMaxWait = 2 * time.Minute
)

// Config holds the Config Agent runtime settings.
type Config struct {
	ConfigServerURL string
	Org             string
	Project         string
	Service         string

	APIKey         string
	DryRun         bool
	ResolveSecrets bool
	HTTPTimeout    time.Duration
	LogLevel       string

	TargetNamespace  string
	TargetConfigMap  string
	TargetSecret     string
	TargetDeployment string
	PollInterval     time.Duration
	DebounceCooldown time.Duration
	DebounceQuiet    time.Duration
	DebounceMaxWait  time.Duration
}

// LoadConfig reads Config Agent settings from env and args. Secrets are env-only
// so they do not appear in process args.
func LoadConfig(args []string, getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	cfg := &Config{
		ConfigServerURL:  firstEnv(getenv, "CONFIG_SERVER_URL", "CONFIG_AGENT_CONFIG_SERVER_URL"),
		Org:              firstEnv(getenv, "CONFIG_AGENT_ORG", "ORG"),
		Project:          firstEnv(getenv, "CONFIG_AGENT_PROJECT", "PROJECT"),
		Service:          firstEnv(getenv, "CONFIG_AGENT_SERVICE", "SERVICE"),
		APIKey:           firstEnv(getenv, "CONFIG_AGENT_API_KEY", "API_KEY"),
		LogLevel:         firstEnvWithDefault(getenv, "info", "CONFIG_AGENT_LOG_LEVEL", "LOG_LEVEL"),
		TargetNamespace:  getenv("CONFIG_AGENT_TARGET_NAMESPACE"),
		TargetConfigMap:  getenv("CONFIG_AGENT_TARGET_CONFIGMAP"),
		TargetSecret:     getenv("CONFIG_AGENT_TARGET_SECRET"),
		TargetDeployment: getenv("CONFIG_AGENT_TARGET_DEPLOYMENT"),
		HTTPTimeout:      defaultHTTPTimeout,
		PollInterval:     defaultPollInterval,
		DebounceCooldown: defaultDebounceCool,
		DebounceQuiet:    defaultDebounceQuiet,
		DebounceMaxWait:  defaultDebounceMaxWait,
	}

	var err error
	if cfg.DryRun, err = envBool(getenv, "CONFIG_AGENT_DRY_RUN", false); err != nil {
		return nil, err
	}
	if cfg.ResolveSecrets, err = envBool(getenv, "CONFIG_AGENT_RESOLVE_SECRETS", false); err != nil {
		return nil, err
	}
	if cfg.HTTPTimeout, err = envDuration(getenv, "CONFIG_AGENT_HTTP_TIMEOUT", cfg.HTTPTimeout); err != nil {
		return nil, err
	}
	if cfg.PollInterval, err = envDuration(getenv, "CONFIG_AGENT_POLL_INTERVAL", cfg.PollInterval); err != nil {
		return nil, err
	}
	if cfg.DebounceCooldown, err = envDuration(getenv, "CONFIG_AGENT_DEBOUNCE_COOLDOWN", cfg.DebounceCooldown); err != nil {
		return nil, err
	}
	if cfg.DebounceQuiet, err = envDuration(getenv, "CONFIG_AGENT_DEBOUNCE_QUIET_PERIOD", cfg.DebounceQuiet); err != nil {
		return nil, err
	}
	if cfg.DebounceMaxWait, err = envDuration(getenv, "CONFIG_AGENT_DEBOUNCE_MAX_WAIT", cfg.DebounceMaxWait); err != nil {
		return nil, err
	}

	fs := flag.NewFlagSet("config-agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.ConfigServerURL, "config-server", cfg.ConfigServerURL, "Config Server base URL")
	fs.StringVar(&cfg.Org, "org", cfg.Org, "Service org")
	fs.StringVar(&cfg.Project, "project", cfg.Project, "Service project")
	fs.StringVar(&cfg.Service, "service", cfg.Service, "Service name")
	fs.BoolVar(&cfg.DryRun, "dry-run", cfg.DryRun, "Fetch from Config Server without applying Kubernetes resources")
	fs.BoolVar(&cfg.ResolveSecrets, "resolve-secrets", cfg.ResolveSecrets, "Fetch env vars with resolve_secrets=true")
	fs.DurationVar(&cfg.HTTPTimeout, "http-timeout", cfg.HTTPTimeout, "Config Server HTTP timeout")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level: debug, info, warn, error")
	fs.StringVar(&cfg.TargetNamespace, "target-namespace", cfg.TargetNamespace, "Target Kubernetes namespace")
	fs.StringVar(&cfg.TargetConfigMap, "target-configmap", cfg.TargetConfigMap, "Target ConfigMap name")
	fs.StringVar(&cfg.TargetSecret, "target-secret", cfg.TargetSecret, "Target Secret name")
	fs.StringVar(&cfg.TargetDeployment, "target-deployment", cfg.TargetDeployment, "Target Deployment name")
	fs.DurationVar(&cfg.PollInterval, "poll-interval", cfg.PollInterval, "Config Server polling interval")
	fs.DurationVar(&cfg.DebounceCooldown, "debounce-cooldown", cfg.DebounceCooldown, "Leading-edge debounce cooldown")
	fs.DurationVar(&cfg.DebounceQuiet, "debounce-quiet-period", cfg.DebounceQuiet, "Leading-edge debounce quiet period")
	fs.DurationVar(&cfg.DebounceMaxWait, "debounce-max-wait", cfg.DebounceMaxWait, "Leading-edge debounce max wait")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate enforces the bootstrap slice's supported runtime contract.
func (c *Config) Validate() error {
	c.trimStrings()
	if c.ConfigServerURL == "" {
		return errors.New("CONFIG_SERVER_URL is required (or pass --config-server)")
	}
	parsed, err := url.Parse(c.ConfigServerURL)
	if err != nil {
		return fmt.Errorf("CONFIG_SERVER_URL is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("CONFIG_SERVER_URL must use http or https")
	}
	if parsed.Host == "" {
		return errors.New("CONFIG_SERVER_URL must include a host")
	}
	if c.Org == "" {
		return errors.New("CONFIG_AGENT_ORG is required (or pass --org)")
	}
	if c.Project == "" {
		return errors.New("CONFIG_AGENT_PROJECT is required (or pass --project)")
	}
	if c.Service == "" {
		return errors.New("CONFIG_AGENT_SERVICE is required (or pass --service)")
	}
	if c.HTTPTimeout <= 0 {
		return fmt.Errorf("CONFIG_AGENT_HTTP_TIMEOUT must be > 0, got %s", c.HTTPTimeout)
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("CONFIG_AGENT_POLL_INTERVAL must be > 0, got %s", c.PollInterval)
	}
	if c.DebounceCooldown <= 0 {
		return fmt.Errorf("CONFIG_AGENT_DEBOUNCE_COOLDOWN must be > 0, got %s", c.DebounceCooldown)
	}
	if c.DebounceQuiet <= 0 {
		return fmt.Errorf("CONFIG_AGENT_DEBOUNCE_QUIET_PERIOD must be > 0, got %s", c.DebounceQuiet)
	}
	if c.DebounceMaxWait <= 0 {
		return fmt.Errorf("CONFIG_AGENT_DEBOUNCE_MAX_WAIT must be > 0, got %s", c.DebounceMaxWait)
	}
	if c.DebounceMaxWait < c.DebounceQuiet {
		return errors.New("CONFIG_AGENT_DEBOUNCE_MAX_WAIT must be >= CONFIG_AGENT_DEBOUNCE_QUIET_PERIOD")
	}
	if c.ResolveSecrets && c.APIKey == "" {
		return errors.New("CONFIG_AGENT_API_KEY or API_KEY is required when --resolve-secrets is enabled")
	}
	if !c.DryRun {
		return errors.New("only --dry-run mode is implemented in this slice")
	}
	return nil
}

func (c *Config) ServiceRef() ServiceRef {
	return ServiceRef{Org: c.Org, Project: c.Project, Service: c.Service}
}

func (c *Config) trimStrings() {
	c.ConfigServerURL = strings.TrimSpace(c.ConfigServerURL)
	c.Org = strings.TrimSpace(c.Org)
	c.Project = strings.TrimSpace(c.Project)
	c.Service = strings.TrimSpace(c.Service)
	c.APIKey = strings.TrimSpace(c.APIKey)
	c.LogLevel = strings.TrimSpace(c.LogLevel)
	c.TargetNamespace = strings.TrimSpace(c.TargetNamespace)
	c.TargetConfigMap = strings.TrimSpace(c.TargetConfigMap)
	c.TargetSecret = strings.TrimSpace(c.TargetSecret)
	c.TargetDeployment = strings.TrimSpace(c.TargetDeployment)
}

func firstEnv(getenv func(string) string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func envBool(getenv func(string) string, key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return value, nil
}

func firstEnvWithDefault(getenv func(string) string, fallback string, keys ...string) string {
	if value := firstEnv(getenv, keys...); value != "" {
		return value
	}
	return fallback
}

func envDuration(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return value, nil
}

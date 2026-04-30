package agent

import (
	"strings"
	"testing"
)

func TestLoadConfigFromEnvAndFlags(t *testing.T) {
	env := map[string]string{
		"CONFIG_SERVER_URL":                  "http://config-server:8080",
		"CONFIG_AGENT_ORG":                   "org",
		"CONFIG_AGENT_PROJECT":               "project",
		"CONFIG_AGENT_SERVICE":               "service",
		"CONFIG_AGENT_DRY_RUN":               "true",
		"CONFIG_AGENT_RESOLVE_SECRETS":       "true",
		"CONFIG_AGENT_API_KEY":               "agent-key",
		"CONFIG_AGENT_TARGET_NAMESPACE":      "ai-platform",
		"CONFIG_AGENT_TARGET_CONFIGMAP":      "litellm-config",
		"CONFIG_AGENT_TARGET_SECRET":         "litellm-env",
		"CONFIG_AGENT_TARGET_DEPLOYMENT":     "litellm",
		"CONFIG_AGENT_POLL_INTERVAL":         "45s",
		"CONFIG_AGENT_DEBOUNCE_COOLDOWN":     "11s",
		"CONFIG_AGENT_DEBOUNCE_QUIET_PERIOD": "12s",
		"CONFIG_AGENT_DEBOUNCE_MAX_WAIT":     "2m",
	}

	cfg, err := LoadConfig([]string{"--service", "override", "--http-timeout", "7s"}, mapGetenv(env))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ConfigServerURL != "http://config-server:8080" {
		t.Fatalf("ConfigServerURL: %q", cfg.ConfigServerURL)
	}
	if cfg.Service != "override" {
		t.Fatalf("Service flag should override env, got %q", cfg.Service)
	}
	if !cfg.DryRun || !cfg.ResolveSecrets {
		t.Fatalf("dry-run and resolve-secrets should be true: %+v", cfg)
	}
	if cfg.APIKey != "agent-key" {
		t.Fatalf("APIKey: %q", cfg.APIKey)
	}
	if cfg.HTTPTimeout.String() != "7s" {
		t.Fatalf("HTTPTimeout: %s", cfg.HTTPTimeout)
	}
	if cfg.TargetNamespace != "ai-platform" || cfg.TargetConfigMap != "litellm-config" ||
		cfg.TargetSecret != "litellm-env" || cfg.TargetDeployment != "litellm" {
		t.Fatalf("target settings not loaded: %+v", cfg)
	}
}

func TestLoadConfigRequiresDryRunInBootstrapSlice(t *testing.T) {
	env := map[string]string{
		"CONFIG_SERVER_URL":    "http://config-server:8080",
		"CONFIG_AGENT_ORG":     "org",
		"CONFIG_AGENT_PROJECT": "project",
		"CONFIG_AGENT_SERVICE": "service",
	}

	_, err := LoadConfig(nil, mapGetenv(env))
	if err == nil || !strings.Contains(err.Error(), "only --dry-run mode is implemented") {
		t.Fatalf("expected dry-run only error, got %v", err)
	}
}

func TestLoadConfigResolveSecretsRequiresAPIKey(t *testing.T) {
	env := map[string]string{
		"CONFIG_SERVER_URL":            "http://config-server:8080",
		"CONFIG_AGENT_ORG":             "org",
		"CONFIG_AGENT_PROJECT":         "project",
		"CONFIG_AGENT_SERVICE":         "service",
		"CONFIG_AGENT_DRY_RUN":         "true",
		"CONFIG_AGENT_RESOLVE_SECRETS": "true",
	}

	_, err := LoadConfig(nil, mapGetenv(env))
	if err == nil || !strings.Contains(err.Error(), "API_KEY") {
		t.Fatalf("expected API key error, got %v", err)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	env := map[string]string{
		"CONFIG_SERVER_URL":         "ftp://config-server",
		"CONFIG_AGENT_ORG":          "org",
		"CONFIG_AGENT_PROJECT":      "project",
		"CONFIG_AGENT_SERVICE":      "service",
		"CONFIG_AGENT_DRY_RUN":      "true",
		"CONFIG_AGENT_HTTP_TIMEOUT": "0s",
	}

	_, err := LoadConfig(nil, mapGetenv(env))
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func mapGetenv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

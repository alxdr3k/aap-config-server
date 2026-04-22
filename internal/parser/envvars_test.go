package parser_test

import (
	"testing"

	"github.com/aap/config-server/internal/parser"
)

func TestParseEnvVars_Basic(t *testing.T) {
	input := `
version: "1"
metadata:
  service: litellm
  org: myorg
  project: ai-platform
env_vars:
  plain:
    LITELLM_LOG_LEVEL: "INFO"
    LITELLM_NUM_WORKERS: "4"
  secret_refs:
    DATABASE_URL: "litellm-db-url"
    LITELLM_MASTER_KEY: "litellm-master-key"
`
	cfg, err := parser.ParseEnvVars([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Metadata.Service != "litellm" {
		t.Errorf("service: want %q, got %q", "litellm", cfg.Metadata.Service)
	}
	if cfg.EnvVars.Plain["LITELLM_LOG_LEVEL"] != "INFO" {
		t.Errorf("LITELLM_LOG_LEVEL: want %q, got %q", "INFO", cfg.EnvVars.Plain["LITELLM_LOG_LEVEL"])
	}
	if cfg.EnvVars.Plain["LITELLM_NUM_WORKERS"] != "4" {
		t.Errorf("LITELLM_NUM_WORKERS: want %q, got %q", "4", cfg.EnvVars.Plain["LITELLM_NUM_WORKERS"])
	}
	if cfg.EnvVars.SecretRefs["DATABASE_URL"] != "litellm-db-url" {
		t.Errorf("DATABASE_URL secret_ref: want %q, got %q", "litellm-db-url", cfg.EnvVars.SecretRefs["DATABASE_URL"])
	}
	if cfg.EnvVars.SecretRefs["LITELLM_MASTER_KEY"] != "litellm-master-key" {
		t.Errorf("LITELLM_MASTER_KEY secret_ref: want %q, got %q", "litellm-master-key", cfg.EnvVars.SecretRefs["LITELLM_MASTER_KEY"])
	}
}

func TestParseEnvVars_EmptyPlain(t *testing.T) {
	input := `
version: "1"
metadata:
  service: svc
  org: org
  project: proj
env_vars:
  secret_refs:
    MY_KEY: "my-secret"
`
	cfg, err := parser.ParseEnvVars([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.EnvVars.Plain) != 0 {
		t.Error("expected nil or empty plain map")
	}
	if cfg.EnvVars.SecretRefs["MY_KEY"] != "my-secret" {
		t.Errorf("MY_KEY: want %q, got %q", "my-secret", cfg.EnvVars.SecretRefs["MY_KEY"])
	}
}

func TestParseEnvVars_InvalidYAML(t *testing.T) {
	_, err := parser.ParseEnvVars([]byte("{ bad: yaml"))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

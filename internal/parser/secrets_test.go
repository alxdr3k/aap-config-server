package parser_test

import (
	"strings"
	"testing"

	"github.com/aap/config-server/internal/parser"
)

func TestParseSecrets_Basic(t *testing.T) {
	input := `
version: "1"
secrets:
  - id: "litellm-master-key"
    description: "LiteLLM master API key"
    k8s_secret:
      name: "litellm-secrets"
      namespace: "ai-platform"
      key: "master-key"
  - id: "azure-gpt4-api-key"
    description: "Azure OpenAI GPT-4 API Key"
    k8s_secret:
      name: "llm-provider-keys"
      namespace: "ai-platform"
      key: "azure-gpt4"
`
	cfg, err := parser.ParseSecrets([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Secrets) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(cfg.Secrets))
	}

	first := cfg.Secrets[0]
	if first.ID != "litellm-master-key" {
		t.Errorf("id: want %q, got %q", "litellm-master-key", first.ID)
	}
	if first.K8sSecret.Name != "litellm-secrets" {
		t.Errorf("k8s name: want %q, got %q", "litellm-secrets", first.K8sSecret.Name)
	}
	if first.K8sSecret.Namespace != "ai-platform" {
		t.Errorf("k8s namespace: want %q, got %q", "ai-platform", first.K8sSecret.Namespace)
	}
	if first.K8sSecret.Key != "master-key" {
		t.Errorf("k8s key: want %q, got %q", "master-key", first.K8sSecret.Key)
	}
}

func TestParseSecrets_Empty(t *testing.T) {
	input := `version: "1"
secrets: []
`
	cfg, err := parser.ParseSecrets([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Secrets) != 0 {
		t.Errorf("expected 0 secrets, got %d", len(cfg.Secrets))
	}
}

func TestParseSecrets_InvalidYAML(t *testing.T) {
	_, err := parser.ParseSecrets([]byte("{[}"))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// TestParseSecrets_RejectsIncompleteEntry covers the P3 review item: every
// secrets.yaml entry must carry a complete k8s_secret pointer so downstream
// secret resolution can't silently operate on a partial reference.
func TestParseSecrets_RejectsIncompleteEntry(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		wantMiss string
	}{
		{
			name: "missing id",
			yaml: `version: "1"
secrets:
  - description: "some"
    k8s_secret:
      name: n
      namespace: ns
      key: k`,
			wantMiss: "id",
		},
		{
			name: "missing k8s_secret.name",
			yaml: `version: "1"
secrets:
  - id: foo
    k8s_secret:
      namespace: ns
      key: k`,
			wantMiss: "k8s_secret.name",
		},
		{
			name: "missing k8s_secret.namespace",
			yaml: `version: "1"
secrets:
  - id: foo
    k8s_secret:
      name: n
      key: k`,
			wantMiss: "k8s_secret.namespace",
		},
		{
			name: "missing k8s_secret.key",
			yaml: `version: "1"
secrets:
  - id: foo
    k8s_secret:
      name: n
      namespace: ns`,
			wantMiss: "k8s_secret.key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parser.ParseSecrets([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected error for missing %s", tc.wantMiss)
			}
			if !strings.Contains(err.Error(), tc.wantMiss) {
				t.Errorf("error should mention %q, got %v", tc.wantMiss, err)
			}
		})
	}
}

func TestParseDefaults_Basic(t *testing.T) {
	input := `
config:
  router_settings:
    routing_strategy: "least-busy"
    num_retries: 3
env_vars:
  plain:
    LITELLM_TELEMETRY: "false"
    LITELLM_LOG_LEVEL: "INFO"
`
	cfg, err := parser.ParseDefaults([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	routerSettings, ok := cfg.Config["router_settings"].(map[string]any)
	if !ok {
		t.Fatal("expected router_settings map")
	}
	if routerSettings["routing_strategy"] != "least-busy" {
		t.Errorf("routing_strategy: want %q, got %v", "least-busy", routerSettings["routing_strategy"])
	}
	if cfg.EnvVars.Plain["LITELLM_TELEMETRY"] != "false" {
		t.Errorf("LITELLM_TELEMETRY: want %q, got %q", "false", cfg.EnvVars.Plain["LITELLM_TELEMETRY"])
	}
}

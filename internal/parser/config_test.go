package parser_test

import (
	"strings"
	"testing"

	"github.com/aap/config-server/internal/parser"
)

func TestParseConfig_Basic(t *testing.T) {
	input := `
version: "1"
metadata:
  service: litellm
  org: myorg
  project: ai-platform
  updated_at: "2026-03-09T10:00:00Z"
config:
  model_list:
    - model_name: "azure-gpt4"
      litellm_params:
        model: "azure/gpt-4"
        api_key_secret_ref: "azure-gpt4-api-key"
  router_settings:
    routing_strategy: "least-busy"
    num_retries: 3
`
	cfg, err := parser.ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Metadata.Service != "litellm" {
		t.Errorf("service: want %q, got %q", "litellm", cfg.Metadata.Service)
	}
	if cfg.Metadata.Org != "myorg" {
		t.Errorf("org: want %q, got %q", "myorg", cfg.Metadata.Org)
	}
	if cfg.Metadata.Project != "ai-platform" {
		t.Errorf("project: want %q, got %q", "ai-platform", cfg.Metadata.Project)
	}
	if cfg.Version != "1" {
		t.Errorf("version: want %q, got %q", "1", cfg.Version)
	}

	models, ok := cfg.Config["model_list"].([]any)
	if !ok || len(models) == 0 {
		t.Fatal("expected model_list with at least one entry")
	}
}

func TestParseConfig_EmptyConfig(t *testing.T) {
	input := `version: "1"
metadata:
  service: svc
  org: org1
  project: proj1
config: {}
`
	cfg, err := parser.ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Config == nil {
		t.Error("expected non-nil config map")
	}
}

func TestParseConfig_InvalidYAML(t *testing.T) {
	_, err := parser.ParseConfig([]byte("{ bad: yaml: ["))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// TestParseConfig_RejectsMissingMetadata covers the P3 review item: semantic
// validation must catch YAML that parses as valid but lacks the identifying
// metadata the store keys on.
func TestParseConfig_RejectsMissingMetadata(t *testing.T) {
	cases := []struct {
		name  string
		yaml  string
		field string
	}{
		{
			name: "missing service",
			yaml: `version: "1"
metadata:
  org: o
  project: p
config: {}`,
			field: "metadata.service",
		},
		{
			name: "missing org",
			yaml: `version: "1"
metadata:
  service: s
  project: p
config: {}`,
			field: "metadata.org",
		},
		{
			name: "missing project",
			yaml: `version: "1"
metadata:
  service: s
  org: o
config: {}`,
			field: "metadata.project",
		},
		{
			name:  "missing whole metadata block",
			yaml:  `version: "1"`,
			field: "metadata",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parser.ParseConfig([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.field)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error should mention %q, got %v", tc.field, err)
			}
		})
	}
}

func TestParseConfig_RejectsSchemaViolations(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unknown top-level field",
			yaml: `version: "1"
metadata:
  service: s
  org: o
  project: p
config: {}
extra: true`,
			want: "unknown field extra in root",
		},
		{
			name: "unknown metadata field",
			yaml: `version: "1"
metadata:
  service: s
  org: o
  project: p
  owner: platform
config: {}`,
			want: "unknown field owner in metadata",
		},
		{
			name: "duplicate top-level field",
			yaml: `version: "1"
metadata:
  service: s
  org: o
  project: p
config: {}
config: {}`,
			want: `duplicate key "config" in root`,
		},
		{
			name: "config is not mapping",
			yaml: `version: "1"
metadata:
  service: s
  org: o
  project: p
config: []`,
			want: "config must be a mapping",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parser.ParseConfig([]byte(tc.yaml))
			if err == nil {
				t.Fatal("expected schema validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should contain %q, got %v", tc.want, err)
			}
		})
	}
}

func TestParseConfig_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		service string
	}{
		{
			name: "minimal valid",
			input: `version: "1"
metadata:
  service: my-svc
  org: o
  project: p
config: {}`,
			service: "my-svc",
		},
		{
			name:    "bad yaml",
			input:   ": - :",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parser.ParseConfig([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.service != "" && cfg.Metadata.Service != tc.service {
				t.Errorf("service: want %q, got %q", tc.service, cfg.Metadata.Service)
			}
		})
	}
}

package agent

import (
	"strings"
	"testing"
)

func TestRenderConfigYAMLPreservesNativeConfigAndSecretRefs(t *testing.T) {
	out, err := RenderConfigYAML(&ConfigSnapshot{
		Config: map[string]any{
			"model_list": []any{
				map[string]any{
					"litellm_params": map[string]any{
						"api_key": "os.environ/AZURE_API_KEY",
						"model":   "azure/gpt-4o",
					},
					"model_name": "gpt-4o",
				},
			},
			"router_settings": map[string]any{
				"cooldown":    0.25,
				"num_retries": float64(3),
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderConfigYAML: %v", err)
	}
	got := string(out)
	want := "model_list:\n  - litellm_params:\n      api_key: os.environ/AZURE_API_KEY\n      model: azure/gpt-4o\n    model_name: gpt-4o\nrouter_settings:\n  cooldown: 0.25\n  num_retries: 3\n"
	if got != want {
		t.Fatalf("rendered yaml:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderConfigYAMLRejectsUnsupportedValues(t *testing.T) {
	_, err := RenderConfigYAML(&ConfigSnapshot{
		Config: map[string]any{"callback": func() {}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported config value type") {
		t.Fatalf("expected unsupported value error, got %v", err)
	}
}

func TestRenderConfigYAMLRejectsNilSnapshot(t *testing.T) {
	_, err := RenderConfigYAML(nil)
	if err == nil || err.Error() != "config snapshot is required" {
		t.Fatalf("expected nil snapshot error, got %v", err)
	}
}

func TestRenderConfigYAMLUsesEmptyMapForNilConfig(t *testing.T) {
	out, err := RenderConfigYAML(&ConfigSnapshot{})
	if err != nil {
		t.Fatalf("RenderConfigYAML: %v", err)
	}
	if string(out) != "{}\n" {
		t.Fatalf("nil config output: %q", out)
	}
}

func TestRenderEnvSHCombinesResolvedValuesDeterministically(t *testing.T) {
	out, err := RenderEnvSH(&EnvVarsSnapshot{
		EnvVars: EnvVars{
			Plain: map[string]string{
				"LOG_LEVEL": "debug",
				"VALUE":     "spaces $and symbols",
			},
			Secrets: map[string]string{
				"API_KEY": "it isn't public",
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderEnvSH: %v", err)
	}
	want := "export API_KEY='it isn'\\''t public'\nexport LOG_LEVEL='debug'\nexport VALUE='spaces $and symbols'\n"
	if string(out) != want {
		t.Fatalf("env.sh:\n%s\nwant:\n%s", out, want)
	}
}

func TestRenderEnvSHRejectsUnresolvedSecretRefs(t *testing.T) {
	_, err := RenderEnvSH(&EnvVarsSnapshot{
		EnvVars: EnvVars{
			Plain:      map[string]string{"LOG_LEVEL": "debug"},
			SecretRefs: map[string]string{"API_KEY": "litellm-api-key"},
		},
	})
	if err == nil || err.Error() != "env vars snapshot contains unresolved secret_refs; fetch with resolve_secrets=true" {
		t.Fatalf("expected unresolved secret refs error, got %v", err)
	}
}

func TestRenderEnvSHRejectsDuplicateKeys(t *testing.T) {
	_, err := RenderEnvSH(&EnvVarsSnapshot{
		EnvVars: EnvVars{
			Plain:   map[string]string{"API_KEY": "plain"},
			Secrets: map[string]string{"API_KEY": "secret"},
		},
	})
	if err == nil || err.Error() != `env var "API_KEY" is defined more than once` {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestRenderEnvSHRejectsInvalidNamesAndNULValues(t *testing.T) {
	tests := []struct {
		name string
		env  EnvVars
		want string
	}{
		{
			name: "invalid name",
			env:  EnvVars{Plain: map[string]string{"1BAD": "value"}},
			want: `plain env var name "1BAD" is invalid`,
		},
		{
			name: "nul value",
			env:  EnvVars{Secrets: map[string]string{"API_KEY": "bad\x00value"}},
			want: `secrets env var "API_KEY" contains NUL byte`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RenderEnvSH(&EnvVarsSnapshot{EnvVars: tt.env})
			if err == nil || err.Error() != tt.want {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

func TestRenderEnvSHRejectsNilSnapshot(t *testing.T) {
	_, err := RenderEnvSH(nil)
	if err == nil || err.Error() != "env vars snapshot is required" {
		t.Fatalf("expected nil snapshot error, got %v", err)
	}
}

package agent

import (
	"context"
	"testing"
	"time"
)

func TestRunDryRunReturnsCountsOnly(t *testing.T) {
	client := fakeSnapshotClient{
		config: &ConfigSnapshot{
			Metadata: Metadata{Version: "abc123", UpdatedAt: time.Date(2026, 4, 30, 1, 2, 3, 0, time.UTC)},
			Config:   map[string]any{"model_list": []any{}, "general_settings": map[string]any{}},
		},
		env: &EnvVarsSnapshot{
			Metadata: Metadata{Version: "abc123", UpdatedAt: time.Date(2026, 4, 30, 1, 2, 3, 0, time.UTC)},
			EnvVars: EnvVars{
				Plain:      map[string]string{"LOG_LEVEL": "INFO"},
				SecretRefs: map[string]string{"UNUSED": "secret-id"},
				Secrets:    map[string]string{"API_KEY": "secret-value"},
			},
		},
	}
	cfg := &Config{
		ConfigServerURL: "http://config-server:8080",
		Org:             "org",
		Project:         "project",
		Service:         "service",
		DryRun:          true,
		ResolveSecrets:  true,
	}

	result, err := RunDryRun(context.Background(), cfg, client)
	if err != nil {
		t.Fatalf("RunDryRun: %v", err)
	}
	if result.Version != "abc123" || result.ConfigKeys != 2 || result.PlainEnvVars != 1 ||
		result.SecretRefs != 1 || result.ResolvedSecrets != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !result.ConfigUpdated || !result.EnvUpdated || !result.ResolveSecrets {
		t.Fatalf("expected update and resolve flags: %+v", result)
	}
}

type fakeSnapshotClient struct {
	config *ConfigSnapshot
	env    *EnvVarsSnapshot
}

func (f fakeSnapshotClient) FetchConfig(context.Context, ServiceRef) (*ConfigSnapshot, error) {
	return f.config, nil
}

func (f fakeSnapshotClient) FetchEnvVars(context.Context, ServiceRef, bool) (*EnvVarsSnapshot, error) {
	return f.env, nil
}

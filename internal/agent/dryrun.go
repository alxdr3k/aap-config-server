package agent

import (
	"context"
	"errors"
)

// SnapshotClient is the Config Server read surface used by the dry-run path.
type SnapshotClient interface {
	FetchConfig(context.Context, ServiceRef) (*ConfigSnapshot, error)
	FetchEnvVars(context.Context, ServiceRef, bool) (*EnvVarsSnapshot, error)
}

// DryRunResult summarizes the data the agent can fetch without applying it.
type DryRunResult struct {
	Ref             ServiceRef
	Version         string
	ConfigUpdated   bool
	EnvUpdated      bool
	ConfigKeys      int
	PlainEnvVars    int
	SecretRefs      int
	ResolvedSecrets int
	ResolveSecrets  bool
}

// RunDryRun fetches config and env var snapshots but does not write Kubernetes
// resources. It intentionally returns counts only, never secret values.
func RunDryRun(ctx context.Context, cfg *Config, client SnapshotClient) (*DryRunResult, error) {
	if cfg == nil {
		return nil, errors.New("agent config is required")
	}
	if client == nil {
		return nil, errors.New("snapshot client is required")
	}
	ref := cfg.ServiceRef()
	configSnapshot, err := client.FetchConfig(ctx, ref)
	if err != nil {
		return nil, err
	}
	envSnapshot, err := client.FetchEnvVars(ctx, ref, cfg.ResolveSecrets)
	if err != nil {
		return nil, err
	}

	result := &DryRunResult{
		Ref:             ref,
		Version:         configSnapshot.Metadata.Version,
		ConfigUpdated:   !configSnapshot.Metadata.UpdatedAt.IsZero(),
		EnvUpdated:      !envSnapshot.Metadata.UpdatedAt.IsZero(),
		ConfigKeys:      len(configSnapshot.Config),
		PlainEnvVars:    len(envSnapshot.EnvVars.Plain),
		SecretRefs:      len(envSnapshot.EnvVars.SecretRefs),
		ResolvedSecrets: len(envSnapshot.EnvVars.Secrets),
		ResolveSecrets:  cfg.ResolveSecrets,
	}
	if result.Version == "" {
		result.Version = envSnapshot.Metadata.Version
	}
	return result, nil
}

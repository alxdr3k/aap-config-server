package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFetchLoopDetectsInitialAndVersionChanges(t *testing.T) {
	client := &sequenceSnapshotClient{
		configs: []*ConfigSnapshot{
			configSnapshot("v1", ts(1)),
			configSnapshot("v1", ts(1)),
			configSnapshot("v2", ts(2)),
		},
		envs: []*EnvVarsSnapshot{
			envSnapshot("v1", ts(1)),
			envSnapshot("v1", ts(1)),
			envSnapshot("v2", ts(2)),
		},
	}
	loop := newTestFetchLoop(t, client)

	first, err := loop.FetchOnce(context.Background())
	if err != nil {
		t.Fatalf("FetchOnce first: %v", err)
	}
	if !first.Initial || !first.Changed || first.State.ConfigVersion != "v1" {
		t.Fatalf("unexpected first result: %+v", first)
	}
	loop.MarkHandled(first)

	second, err := loop.FetchOnce(context.Background())
	if err != nil {
		t.Fatalf("FetchOnce second: %v", err)
	}
	if second.Initial || second.Changed {
		t.Fatalf("second result should be unchanged: %+v", second)
	}

	third, err := loop.FetchOnce(context.Background())
	if err != nil {
		t.Fatalf("FetchOnce third: %v", err)
	}
	if third.Initial || !third.Changed || third.State.EnvVersion != "v2" {
		t.Fatalf("third result should be changed: %+v", third)
	}
}

func TestFetchLoopDoesNotMarkHandledUntilSuccess(t *testing.T) {
	client := &sequenceSnapshotClient{
		configs: []*ConfigSnapshot{
			configSnapshot("v1", ts(1)),
			configSnapshot("v1", ts(1)),
			configSnapshot("v1", ts(1)),
		},
		envs: []*EnvVarsSnapshot{
			envSnapshot("v1", ts(1)),
			envSnapshot("v1", ts(1)),
			envSnapshot("v1", ts(1)),
		},
	}
	loop := newTestFetchLoop(t, client)

	first, err := loop.FetchOnce(context.Background())
	if err != nil {
		t.Fatalf("FetchOnce first: %v", err)
	}
	if !first.Changed {
		t.Fatalf("first result should be changed")
	}

	second, err := loop.FetchOnce(context.Background())
	if err != nil {
		t.Fatalf("FetchOnce second: %v", err)
	}
	if !second.Changed {
		t.Fatalf("same version should still be changed before MarkHandled")
	}
	loop.MarkHandled(second)

	third, err := loop.FetchOnce(context.Background())
	if err != nil {
		t.Fatalf("FetchOnce third: %v", err)
	}
	if third.Changed {
		t.Fatalf("same version should be unchanged after MarkHandled")
	}
}

func TestFetchLoopRunUsesRetryBackoffAndPollInterval(t *testing.T) {
	client := &sequenceSnapshotClient{
		configErrs: []error{errors.New("temporary config read failure")},
		configs:    []*ConfigSnapshot{configSnapshot("v1", ts(1))},
		envs:       []*EnvVarsSnapshot{envSnapshot("v1", ts(1))},
	}
	loop := newTestFetchLoop(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waits []time.Duration
	loop.wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		if len(waits) == 2 {
			cancel()
			return context.Canceled
		}
		return nil
	}

	var handled int
	err := loop.Run(ctx, func(context.Context, FetchResult) error {
		handled++
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if handled != 1 {
		t.Fatalf("handled count: got %d, want 1", handled)
	}
	if len(waits) != 2 || waits[0] != loop.cfg.RetryInitialBackoff || waits[1] != loop.cfg.PollInterval {
		t.Fatalf("waits: got %+v", waits)
	}
}

func TestFetchLoopPassesResolveSecrets(t *testing.T) {
	client := &sequenceSnapshotClient{
		configs: []*ConfigSnapshot{configSnapshot("v1", ts(1))},
		envs:    []*EnvVarsSnapshot{envSnapshot("v1", ts(1))},
	}
	loop := newTestFetchLoop(t, client)
	loop.cfg.ResolveSecrets = true

	result, err := loop.FetchOnce(context.Background())
	if err != nil {
		t.Fatalf("FetchOnce: %v", err)
	}
	if !result.ResolveSecrets || len(client.resolveSecrets) != 1 || !client.resolveSecrets[0] {
		t.Fatalf("resolve_secrets not propagated: result=%+v calls=%+v", result, client.resolveSecrets)
	}
}

func TestFetchLoopRejectsNilSnapshots(t *testing.T) {
	client := &sequenceSnapshotClient{
		configs: []*ConfigSnapshot{nil},
	}
	loop := newTestFetchLoop(t, client)

	_, err := loop.FetchOnce(context.Background())
	if err == nil || err.Error() != "config snapshot is nil" {
		t.Fatalf("expected nil config snapshot error, got %v", err)
	}
}

func TestRetryBackoffDoublesAndCaps(t *testing.T) {
	backoff, err := NewRetryBackoff(time.Second, 3*time.Second)
	if err != nil {
		t.Fatalf("NewRetryBackoff: %v", err)
	}
	if got := backoff.Next(); got != time.Second {
		t.Fatalf("first backoff: %s", got)
	}
	if got := backoff.Next(); got != 2*time.Second {
		t.Fatalf("second backoff: %s", got)
	}
	if got := backoff.Next(); got != 3*time.Second {
		t.Fatalf("capped backoff: %s", got)
	}
	backoff.Reset()
	if got := backoff.Next(); got != time.Second {
		t.Fatalf("reset backoff: %s", got)
	}
}

func newTestFetchLoop(t *testing.T, client SnapshotClient) *FetchLoop {
	t.Helper()
	loop, err := NewFetchLoop(client, FetchLoopConfig{
		Ref: ServiceRef{
			Org:     "org",
			Project: "project",
			Service: "service",
		},
		PollInterval:        30 * time.Second,
		RetryInitialBackoff: time.Second,
		RetryMaxBackoff:     4 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewFetchLoop: %v", err)
	}
	return loop
}

type sequenceSnapshotClient struct {
	configs        []*ConfigSnapshot
	envs           []*EnvVarsSnapshot
	configErrs     []error
	envErrs        []error
	resolveSecrets []bool
}

func (c *sequenceSnapshotClient) FetchConfig(context.Context, ServiceRef) (*ConfigSnapshot, error) {
	if len(c.configErrs) > 0 {
		err := c.configErrs[0]
		c.configErrs = c.configErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(c.configs) == 0 {
		return nil, errors.New("no config snapshot queued")
	}
	snapshot := c.configs[0]
	c.configs = c.configs[1:]
	return snapshot, nil
}

func (c *sequenceSnapshotClient) FetchEnvVars(_ context.Context, _ ServiceRef, resolveSecrets bool) (*EnvVarsSnapshot, error) {
	c.resolveSecrets = append(c.resolveSecrets, resolveSecrets)
	if len(c.envErrs) > 0 {
		err := c.envErrs[0]
		c.envErrs = c.envErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(c.envs) == 0 {
		return nil, errors.New("no env snapshot queued")
	}
	snapshot := c.envs[0]
	c.envs = c.envs[1:]
	return snapshot, nil
}

func configSnapshot(version string, updatedAt time.Time) *ConfigSnapshot {
	return &ConfigSnapshot{
		Metadata: Metadata{Version: version, UpdatedAt: updatedAt},
		Config:   map[string]any{"model_list": []any{}},
	}
}

func envSnapshot(version string, updatedAt time.Time) *EnvVarsSnapshot {
	return &EnvVarsSnapshot{
		Metadata: Metadata{Version: version, UpdatedAt: updatedAt},
		EnvVars:  EnvVars{Plain: map[string]string{"LOG_LEVEL": "INFO"}},
	}
}

func ts(second int) time.Time {
	return time.Date(2026, 4, 30, 1, 2, second, 0, time.UTC)
}

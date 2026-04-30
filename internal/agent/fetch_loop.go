package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	defaultFetchRetryInitialBackoff = time.Second
	defaultFetchRetryMaxBackoff     = 30 * time.Second
)

// FetchLoopConfig controls read-API polling for one service.
type FetchLoopConfig struct {
	Ref                 ServiceRef
	ResolveSecrets      bool
	PollInterval        time.Duration
	RetryInitialBackoff time.Duration
	RetryMaxBackoff     time.Duration
}

// FetchState tracks the last successfully handled Config Server versions.
type FetchState struct {
	Revision        string
	ConfigHash      string
	EnvHash         string
	ConfigUpdatedAt time.Time
	EnvUpdatedAt    time.Time
	Handled         bool
}

// FetchResult is one Config Server read poll result.
type FetchResult struct {
	Ref            ServiceRef
	Config         *ConfigSnapshot
	EnvVars        *EnvVarsSnapshot
	State          FetchState
	Changed        bool
	Initial        bool
	ResolveSecrets bool
}

// FetchHandler receives changed snapshots. The loop marks a version as handled
// only after this function succeeds.
type FetchHandler func(context.Context, FetchResult) error

// FetchLoop polls Config Server read APIs and tracks the last handled version.
type FetchLoop struct {
	client SnapshotClient
	cfg    FetchLoopConfig
	state  FetchState
	wait   func(context.Context, time.Duration) error
}

// NewFetchLoop creates a read-API polling loop.
func NewFetchLoop(client SnapshotClient, cfg FetchLoopConfig) (*FetchLoop, error) {
	if client == nil {
		return nil, errors.New("snapshot client is required")
	}
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &FetchLoop{
		client: client,
		cfg:    cfg,
		wait:   waitContext,
	}, nil
}

// FetchOnce reads config/env snapshots and compares them against the last
// successfully handled state. It does not mutate loop state.
func (l *FetchLoop) FetchOnce(ctx context.Context) (FetchResult, error) {
	if l == nil {
		return FetchResult{}, errors.New("fetch loop is required")
	}
	configSnapshot, err := l.client.FetchConfig(ctx, l.cfg.Ref)
	if err != nil {
		return FetchResult{}, err
	}
	if configSnapshot == nil {
		return FetchResult{}, errors.New("config snapshot is nil")
	}
	envSnapshot, err := l.client.FetchEnvVars(ctx, l.cfg.Ref, l.cfg.ResolveSecrets)
	if err != nil {
		return FetchResult{}, err
	}
	if envSnapshot == nil {
		return FetchResult{}, errors.New("env vars snapshot is nil")
	}
	if configSnapshot.Metadata.Version != envSnapshot.Metadata.Version {
		return FetchResult{}, fmt.Errorf("config/env revision mismatch: config=%q env=%q",
			configSnapshot.Metadata.Version, envSnapshot.Metadata.Version)
	}
	configHash, err := hashJSON(configSnapshot.Config)
	if err != nil {
		return FetchResult{}, fmt.Errorf("hash config snapshot: %w", err)
	}
	envHash, err := hashJSON(envSnapshot.EnvVars)
	if err != nil {
		return FetchResult{}, fmt.Errorf("hash env vars snapshot: %w", err)
	}

	state := FetchState{
		Revision:        configSnapshot.Metadata.Version,
		ConfigHash:      configHash,
		EnvHash:         envHash,
		ConfigUpdatedAt: configSnapshot.Metadata.UpdatedAt,
		EnvUpdatedAt:    envSnapshot.Metadata.UpdatedAt,
		Handled:         true,
	}
	initial := !l.state.Handled
	changed := initial || !l.state.sameContent(state)
	return FetchResult{
		Ref:            l.cfg.Ref,
		Config:         configSnapshot,
		EnvVars:        envSnapshot,
		State:          state,
		Changed:        changed,
		Initial:        initial,
		ResolveSecrets: l.cfg.ResolveSecrets,
	}, nil
}

// MarkHandled records the result's version as successfully handled.
func (l *FetchLoop) MarkHandled(result FetchResult) {
	if l == nil {
		return
	}
	l.state = result.State
}

// State returns the last successfully handled version state.
func (l *FetchLoop) State() FetchState {
	if l == nil {
		return FetchState{}
	}
	return l.state
}

// Run polls until ctx is cancelled. Fetch or handler errors use exponential
// backoff; successful polls reset backoff and wait PollInterval.
func (l *FetchLoop) Run(ctx context.Context, handler FetchHandler) error {
	if l == nil {
		return errors.New("fetch loop is required")
	}
	if ctx == nil {
		return errors.New("context is required")
	}
	if handler == nil {
		handler = func(context.Context, FetchResult) error { return nil }
	}
	backoff, err := NewRetryBackoff(l.cfg.RetryInitialBackoff, l.cfg.RetryMaxBackoff)
	if err != nil {
		return err
	}

	for {
		result, err := l.FetchOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if err := l.wait(ctx, backoff.Next()); err != nil {
				return nilIfContextDone(ctx, err)
			}
			continue
		}

		if result.Changed {
			if err := handler(ctx, result); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				if err := l.wait(ctx, backoff.Next()); err != nil {
					return nilIfContextDone(ctx, err)
				}
				continue
			}
			l.MarkHandled(result)
		}
		backoff.Reset()
		if err := l.wait(ctx, l.cfg.PollInterval); err != nil {
			return nilIfContextDone(ctx, err)
		}
	}
}

// RetryBackoff doubles delays up to a configured cap.
type RetryBackoff struct {
	initial time.Duration
	max     time.Duration
	current time.Duration
}

func NewRetryBackoff(initial, max time.Duration) (*RetryBackoff, error) {
	if initial <= 0 {
		return nil, fmt.Errorf("retry initial backoff must be > 0, got %s", initial)
	}
	if max <= 0 {
		return nil, fmt.Errorf("retry max backoff must be > 0, got %s", max)
	}
	if max < initial {
		return nil, errors.New("retry max backoff must be >= initial backoff")
	}
	return &RetryBackoff{initial: initial, max: max}, nil
}

func (b *RetryBackoff) Next() time.Duration {
	if b.current == 0 {
		b.current = b.initial
		return b.current
	}
	next := b.current * 2
	if next > b.max {
		next = b.max
	}
	b.current = next
	return b.current
}

func (b *RetryBackoff) Reset() {
	b.current = 0
}

func (c FetchLoopConfig) Validate() error {
	if err := c.Ref.Validate(); err != nil {
		return err
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("poll interval must be > 0, got %s", c.PollInterval)
	}
	if c.RetryInitialBackoff <= 0 {
		return fmt.Errorf("retry initial backoff must be > 0, got %s", c.RetryInitialBackoff)
	}
	if c.RetryMaxBackoff <= 0 {
		return fmt.Errorf("retry max backoff must be > 0, got %s", c.RetryMaxBackoff)
	}
	if c.RetryMaxBackoff < c.RetryInitialBackoff {
		return errors.New("retry max backoff must be >= initial backoff")
	}
	return nil
}

func (c FetchLoopConfig) withDefaults() FetchLoopConfig {
	c.Ref = c.Ref.Normalized()
	if c.PollInterval == 0 {
		c.PollInterval = defaultPollInterval
	}
	if c.RetryInitialBackoff == 0 {
		c.RetryInitialBackoff = defaultFetchRetryInitialBackoff
	}
	if c.RetryMaxBackoff == 0 {
		c.RetryMaxBackoff = defaultFetchRetryMaxBackoff
	}
	return c
}

func (s FetchState) sameContent(other FetchState) bool {
	return s.ConfigHash == other.ConfigHash && s.EnvHash == other.EnvHash
}

func hashJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nilIfContextDone(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

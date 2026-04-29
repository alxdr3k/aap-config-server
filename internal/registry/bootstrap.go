package registry

import (
	"context"
	"fmt"
	"time"
)

const (
	DefaultBootstrapAttempts       = 5
	DefaultBootstrapInitialBackoff = time.Second
	DefaultBootstrapMaxBackoff     = 30 * time.Second
)

// SleepFunc sleeps for d or returns when ctx is cancelled.
type SleepFunc func(ctx context.Context, d time.Duration) error

// BootstrapOptions controls startup registry loading.
type BootstrapOptions struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Sleep          SleepFunc
	Now            func() time.Time
}

// BootstrapResult reports the startup load outcome.
type BootstrapResult struct {
	Loaded     bool
	Skipped    bool
	Attempts   int
	AppsLoaded int
	Err        error
}

// Bootstrap loads the Console App Registry into cache with bounded
// exponential backoff. Final failure preserves the existing cache, which is
// empty during normal startup.
func Bootstrap(ctx context.Context, cache *Cache, loader Loader, opts BootstrapOptions) BootstrapResult {
	opts = opts.withDefaults()
	if loader == nil {
		err := fmt.Errorf("registry loader is required")
		cache.MarkLoadFailed(err)
		return BootstrapResult{Err: err}
	}

	var lastErr error
	for attempt := 1; attempt <= opts.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			cache.MarkLoadFailed(err)
			return BootstrapResult{Attempts: attempt - 1, Err: err}
		}
		apps, err := loader.LoadApps(ctx)
		if err == nil {
			cache.Replace(apps, opts.Now())
			return BootstrapResult{
				Loaded:     true,
				Attempts:   attempt,
				AppsLoaded: len(apps),
			}
		}
		lastErr = err
		if attempt == opts.MaxAttempts {
			break
		}
		backoff := opts.backoff(attempt)
		if err := opts.Sleep(ctx, backoff); err != nil {
			cache.MarkLoadFailed(err)
			return BootstrapResult{Attempts: attempt, Err: err}
		}
	}
	cache.MarkLoadFailed(lastErr)
	return BootstrapResult{Attempts: opts.MaxAttempts, Err: lastErr}
}

func (o BootstrapOptions) withDefaults() BootstrapOptions {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = DefaultBootstrapAttempts
	}
	if o.InitialBackoff <= 0 {
		o.InitialBackoff = DefaultBootstrapInitialBackoff
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = DefaultBootstrapMaxBackoff
	}
	if o.MaxBackoff < o.InitialBackoff {
		o.MaxBackoff = o.InitialBackoff
	}
	if o.Sleep == nil {
		o.Sleep = sleepContext
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	return o
}

func (o BootstrapOptions) backoff(attempt int) time.Duration {
	backoff := o.InitialBackoff
	for i := 1; i < attempt; i++ {
		if backoff >= o.MaxBackoff/2 {
			return o.MaxBackoff
		}
		backoff *= 2
	}
	if backoff > o.MaxBackoff {
		return o.MaxBackoff
	}
	return backoff
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

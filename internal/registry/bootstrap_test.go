package registry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aap/config-server/internal/registry"
)

type fakeLoader struct {
	results []loaderResult
	calls   int
}

type loaderResult struct {
	apps []registry.App
	err  error
}

func (f *fakeLoader) LoadApps(context.Context) ([]registry.App, error) {
	i := f.calls
	f.calls++
	if i >= len(f.results) {
		return nil, errors.New("unexpected call")
	}
	return f.results[i].apps, f.results[i].err
}

func TestBootstrap_RetriesWithBoundedBackoffAndLoadsCache(t *testing.T) {
	loader := &fakeLoader{results: []loaderResult{
		{err: errors.New("first")},
		{err: errors.New("second")},
		{apps: []registry.App{{Org: "myorg", Project: "ai", Service: "litellm"}}},
	}}
	cache := registry.NewCache()
	var sleeps []time.Duration
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)

	result := registry.Bootstrap(context.Background(), cache, loader, registry.BootstrapOptions{
		MaxAttempts:    5,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     15 * time.Millisecond,
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
		Now: func() time.Time { return now },
	})

	if !result.Loaded || result.Attempts != 3 || result.AppsLoaded != 1 {
		t.Fatalf("result: %+v", result)
	}
	if loader.calls != 3 {
		t.Fatalf("loader calls: got %d", loader.calls)
	}
	if len(sleeps) != 2 || sleeps[0] != 10*time.Millisecond || sleeps[1] != 15*time.Millisecond {
		t.Fatalf("sleeps: got %v", sleeps)
	}
	apps := cache.List()
	if len(apps) != 1 || apps[0].Service != "litellm" {
		t.Fatalf("cached apps: %+v", apps)
	}
	status := cache.Status()
	if status.AppsLoaded != 1 || !status.LastLoadedAt.Equal(now) || status.LastLoadError != "" ||
		status.State != "ok" || status.IsDegraded {
		t.Fatalf("status: %+v", status)
	}
}

func TestBootstrap_FinalFailureKeepsEmptyCache(t *testing.T) {
	loader := &fakeLoader{results: []loaderResult{
		{err: errors.New("first")},
		{err: errors.New("second")},
	}}
	cache := registry.NewCache()

	result := registry.Bootstrap(context.Background(), cache, loader, registry.BootstrapOptions{
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		Sleep:          func(context.Context, time.Duration) error { return nil },
	})

	if result.Loaded || result.Attempts != 2 || result.Err == nil {
		t.Fatalf("result: %+v", result)
	}
	if apps := cache.List(); len(apps) != 0 {
		t.Fatalf("expected empty cache, got %+v", apps)
	}
	if status := cache.Status(); status.LastLoadError == "" || status.State != "degraded" || !status.IsDegraded {
		t.Fatalf("expected last load error, got %+v", status)
	}
}

func TestBootstrap_ContextCancelStopsBetweenAttempts(t *testing.T) {
	loader := &fakeLoader{results: []loaderResult{
		{err: errors.New("first")},
		{apps: []registry.App{{Org: "myorg", Project: "ai", Service: "litellm"}}},
	}}
	cache := registry.NewCache()
	ctx, cancel := context.WithCancel(context.Background())

	result := registry.Bootstrap(ctx, cache, loader, registry.BootstrapOptions{
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		Sleep: func(context.Context, time.Duration) error {
			cancel()
			return context.Canceled
		},
	})

	if result.Loaded || result.Attempts != 1 || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("result: %+v", result)
	}
	if loader.calls != 1 {
		t.Fatalf("loader calls: got %d", loader.calls)
	}
}

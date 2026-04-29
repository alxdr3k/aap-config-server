package registry_test

import (
	"testing"
	"time"

	"github.com/aap/config-server/internal/registry"
)

func TestCache_UpsertIgnoresStaleEvent(t *testing.T) {
	cache := registry.NewCache()
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	newer := registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		UpdatedAt: "2026-04-29T10:00:00Z",
	}
	if _, changed, err := cache.Upsert(newer, now); err != nil || !changed {
		t.Fatalf("upsert newer: changed=%v err=%v", changed, err)
	}

	stale := registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		Name:      "stale-name",
		UpdatedAt: "2026-04-29T09:00:00Z",
	}
	if _, changed, err := cache.Upsert(stale, now.Add(time.Minute)); err != nil || changed {
		t.Fatalf("upsert stale: changed=%v err=%v", changed, err)
	}
	apps := cache.List()
	if len(apps) != 1 || apps[0].Name != "litellm" || apps[0].UpdatedAt != newer.UpdatedAt {
		t.Fatalf("stale event replaced cache: %+v", apps)
	}
}

func TestCache_DeleteIgnoresStaleEvent(t *testing.T) {
	cache := registry.NewCache()
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	current := registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		UpdatedAt: "2026-04-29T10:00:00Z",
	}
	if _, changed, err := cache.Upsert(current, now); err != nil || !changed {
		t.Fatalf("upsert current: changed=%v err=%v", changed, err)
	}

	staleDelete := registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		UpdatedAt: "2026-04-29T09:00:00Z",
	}
	if _, deleted, err := cache.Delete(staleDelete, now.Add(time.Minute)); err != nil || deleted {
		t.Fatalf("delete stale: deleted=%v err=%v", deleted, err)
	}
	if apps := cache.List(); len(apps) != 1 {
		t.Fatalf("stale delete removed cache entry: %+v", apps)
	}
}

func TestCache_RejectsInvalidEventTimestamp(t *testing.T) {
	cache := registry.NewCache()
	_, _, err := cache.Upsert(registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		UpdatedAt: "yesterday",
	}, time.Now())
	if err == nil {
		t.Fatal("expected invalid updated_at error")
	}
}

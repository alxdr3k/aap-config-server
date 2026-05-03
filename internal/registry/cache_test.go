package registry_test

import (
	"errors"
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

func TestCache_DeleteWatermarkRejectsOlderUpsertAfterRemoval(t *testing.T) {
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

	deleteEvent := registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		UpdatedAt: "2026-04-29T11:00:00Z",
	}
	if _, deleted, err := cache.Delete(deleteEvent, now.Add(time.Minute)); err != nil || !deleted {
		t.Fatalf("delete current: deleted=%v err=%v", deleted, err)
	}

	staleUpsert := registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		Name:      "stale-name",
		UpdatedAt: "2026-04-29T10:30:00Z",
	}
	if _, changed, err := cache.Upsert(staleUpsert, now.Add(2*time.Minute)); err != nil || changed {
		t.Fatalf("upsert stale after delete: changed=%v err=%v", changed, err)
	}
	if apps := cache.List(); len(apps) != 0 {
		t.Fatalf("stale upsert resurrected deleted entry: %+v", apps)
	}

	freshUpsert := registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		Name:      "fresh-name",
		UpdatedAt: "2026-04-29T12:00:00Z",
	}
	if _, changed, err := cache.Upsert(freshUpsert, now.Add(3*time.Minute)); err != nil || !changed {
		t.Fatalf("upsert fresh after delete: changed=%v err=%v", changed, err)
	}
	apps := cache.List()
	if len(apps) != 1 || apps[0].Name != "fresh-name" {
		t.Fatalf("fresh upsert was not applied: %+v", apps)
	}
}

func TestCache_DeleteMissingEntryWatermarkRejectsOlderUpsert(t *testing.T) {
	cache := registry.NewCache()
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	deleteEvent := registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		UpdatedAt: "2026-04-29T11:00:00Z",
	}
	if _, deleted, err := cache.Delete(deleteEvent, now); err != nil || deleted {
		t.Fatalf("delete missing: deleted=%v err=%v", deleted, err)
	}

	staleUpsert := registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		UpdatedAt: "2026-04-29T10:00:00Z",
	}
	if _, changed, err := cache.Upsert(staleUpsert, now.Add(time.Minute)); err != nil || changed {
		t.Fatalf("upsert stale after missing delete: changed=%v err=%v", changed, err)
	}
	if apps := cache.List(); len(apps) != 0 {
		t.Fatalf("stale upsert recreated missing entry: %+v", apps)
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

func TestCache_StatusStartsNotConfigured(t *testing.T) {
	status := registry.NewCache().Status()
	if status.State != "not_configured" || status.IsDegraded {
		t.Fatalf("status: %+v", status)
	}
}

func TestCache_WebhookUpdateDoesNotClearLoadFailure(t *testing.T) {
	cache := registry.NewCache()
	cache.MarkLoadFailed(errors.New("console unavailable"))
	updatedAt := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)

	if _, changed, err := cache.Upsert(registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		UpdatedAt: "2026-04-29T10:00:00Z",
	}, updatedAt); err != nil || !changed {
		t.Fatalf("upsert after load failure: changed=%v err=%v", changed, err)
	}

	status := cache.Status()
	if status.State != "degraded" || !status.IsDegraded || status.LastLoadError == "" {
		t.Fatalf("load failure should remain visible after webhook update: %+v", status)
	}
	if status.AppsLoaded != 1 || !status.LastUpdatedAt.Equal(updatedAt) {
		t.Fatalf("cache update state not recorded: %+v", status)
	}
}

func TestCache_WebhookUpdatePreservesNotConfiguredStatus(t *testing.T) {
	cache := registry.NewCache()
	updatedAt := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)

	if _, changed, err := cache.Upsert(registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		UpdatedAt: "2026-04-29T10:00:00Z",
	}, updatedAt); err != nil || !changed {
		t.Fatalf("upsert after skipped load: changed=%v err=%v", changed, err)
	}

	status := cache.Status()
	if status.State != "not_configured" || status.IsDegraded {
		t.Fatalf("webhook should not imply full-load health: %+v", status)
	}
	if status.AppsLoaded != 1 || !status.LastUpdatedAt.Equal(updatedAt) {
		t.Fatalf("cache update state not recorded: %+v", status)
	}
}

func TestCache_ReplaceKeepsTombstoneWithinRetention(t *testing.T) {
	// A vanished key's watermark is preserved across Replace within the
	// retention window so stale pre-bootstrap webhook events cannot
	// resurrect a deleted app.
	cache := registry.NewCache()
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)

	if _, _, err := cache.Upsert(registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "ephemeral",
		UpdatedAt: now.Format(time.RFC3339),
	}, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	deleteAt := now.Add(time.Second)
	if _, _, err := cache.Delete(registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "ephemeral",
		UpdatedAt: deleteAt.Format(time.RFC3339),
	}, deleteAt); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Bootstrap snapshot at +1m omits the deleted app.
	bootstrapAt := now.Add(time.Minute)
	cache.Replace([]registry.App{{
		Org:       "myorg",
		Project:   "ai",
		Service:   "kept",
		UpdatedAt: bootstrapAt.Format(time.RFC3339),
	}}, bootstrapAt)

	// Stale Upsert for the vanished key (event_at < deleteAt) must be
	// rejected — the tombstone watermark is still in retention.
	staleAt := now.Add(-time.Hour)
	_, changed, err := cache.Upsert(registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "ephemeral",
		UpdatedAt: staleAt.Format(time.RFC3339),
	}, bootstrapAt.Add(time.Second))
	if err != nil {
		t.Fatalf("stale upsert: %v", err)
	}
	if changed {
		t.Fatalf("stale pre-tombstone Upsert resurrected a deleted app: tombstone retention not honored")
	}
}

func TestCache_UpsertGCsExpiredTombstonesWithoutReplace(t *testing.T) {
	// Steady-state production runs only Upsert/Delete (webhook traffic) after
	// the initial bootstrap. Tombstone GC therefore must run inline on
	// Upsert/Delete, not just inside Replace.
	cache := registry.NewCache()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Seed two tombstones in the past.
	for _, name := range []string{"gone-a", "gone-b"} {
		if _, _, err := cache.Upsert(registry.App{
			Org:       "myorg",
			Project:   "ai",
			Service:   name,
			UpdatedAt: old.Format(time.RFC3339),
		}, old); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		if _, _, err := cache.Delete(registry.App{
			Org:       "myorg",
			Project:   "ai",
			Service:   name,
			UpdatedAt: old.Add(time.Second).Format(time.RFC3339),
		}, old.Add(time.Second)); err != nil {
			t.Fatalf("delete %s: %v", name, err)
		}
	}

	// Two days later, a brand-new app comes through Upsert. Both old
	// tombstones are now past the 24h retention and should be GC'd inline.
	muchLater := old.Add(48 * time.Hour)
	if _, _, err := cache.Upsert(registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "fresh",
		UpdatedAt: muchLater.Format(time.RFC3339),
	}, muchLater); err != nil {
		t.Fatalf("upsert fresh: %v", err)
	}

	// A "stale" Upsert against the long-gone keys should now succeed
	// because their tombstones were GC'd by the inline prune in `Upsert`.
	for _, name := range []string{"gone-a", "gone-b"} {
		_, changed, err := cache.Upsert(registry.App{
			Org:       "myorg",
			Project:   "ai",
			Service:   name,
			UpdatedAt: muchLater.Format(time.RFC3339),
		}, muchLater.Add(time.Second)); if err != nil {
			t.Fatalf("post-gc upsert %s: %v", name, err)
		}
		if !changed {
			t.Fatalf("expected long-aged tombstone for %s to be GC'd by inline prune", name)
		}
	}
}

func TestCache_ReplaceDropsTombstonesOlderThanRetention(t *testing.T) {
	// After retention has elapsed without any new event, the tombstone is
	// dropped on the next Replace so memory does not grow unbounded under
	// high-churn workloads (e.g. ephemeral PR-preview apps).
	cache := registry.NewCache()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if _, _, err := cache.Upsert(registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "long-gone",
		UpdatedAt: old.Format(time.RFC3339),
	}, old); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, _, err := cache.Delete(registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "long-gone",
		UpdatedAt: old.Add(time.Second).Format(time.RFC3339),
	}, old.Add(time.Second)); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Bootstrap two days later — tombstone aged past the 24h retention.
	muchLater := old.Add(48 * time.Hour)
	cache.Replace([]registry.App{}, muchLater)

	// A "stale" Upsert is now accepted because the tombstone has been
	// GC'd. This is the bounded-memory tradeoff documented in the cache.
	_, changed, err := cache.Upsert(registry.App{
		Org:       "myorg",
		Project:   "ai",
		Service:   "long-gone",
		UpdatedAt: muchLater.Format(time.RFC3339),
	}, muchLater.Add(time.Second))
	if err != nil {
		t.Fatalf("post-retention upsert: %v", err)
	}
	if !changed {
		t.Fatalf("tombstone past retention should have been dropped, expected post-retention upsert to land")
	}
}

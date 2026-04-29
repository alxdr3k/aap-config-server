package registry

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	cacheStatusNotConfigured = "not_configured"
	cacheStatusOK            = "ok"
	cacheStatusDegraded      = "degraded"
)

// Cache stores the last Console App Registry snapshot in memory.
type Cache struct {
	mu          sync.RWMutex
	apps        map[Key]App
	lastEventAt map[Key]time.Time
	lastLoaded  time.Time
	lastUpdated time.Time
	lastLoadErr error
	status      string
}

// Status reports cache load state without exposing mutable cache internals.
type Status struct {
	AppsLoaded    int
	LastLoadedAt  time.Time
	LastUpdatedAt time.Time
	LastLoadError string
	State         string
	IsDegraded    bool
}

// NewCache creates an empty registry cache.
func NewCache() *Cache {
	return &Cache{
		apps:        map[Key]App{},
		lastEventAt: map[Key]time.Time{},
		status:      cacheStatusNotConfigured,
	}
}

// Replace atomically replaces the cached registry snapshot.
func (c *Cache) Replace(apps []App, loadedAt time.Time) {
	if c == nil {
		return
	}
	normalized := make(map[Key]App, len(apps))
	snapshotEventAt := make(map[Key]time.Time, len(apps))
	for _, app := range apps {
		next, err := normalizeApp(app)
		if err != nil {
			continue
		}
		key := keyFor(next)
		normalized[key] = next
		if eventAt, ok, err := parseEventTime(next.UpdatedAt); err == nil && ok {
			snapshotEventAt[key] = eventAt
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureMapsLocked()
	c.apps = normalized
	for key, eventAt := range snapshotEventAt {
		rememberEventTime(c.lastEventAt, key, eventAt)
	}
	c.lastLoaded = loadedAt.UTC()
	c.lastUpdated = loadedAt.UTC()
	c.lastLoadErr = nil
	c.status = cacheStatusOK
}

// Upsert inserts or replaces one app registration. If app.UpdatedAt is set
// and the existing cache entry has a newer UpdatedAt, the stale event is
// ignored.
func (c *Cache) Upsert(app App, updatedAt time.Time) (App, bool, error) {
	normalized, err := normalizeApp(app)
	if err != nil {
		return App{}, false, err
	}
	eventAt, err := requireEventTime(normalized)
	if err != nil {
		return App{}, false, err
	}
	if c == nil {
		return normalized, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureMapsLocked()
	key := keyFor(normalized)
	if watermark, ok := c.lastEventAt[key]; ok && eventAt.Before(watermark) {
		if current, ok := c.apps[key]; ok {
			return current, false, nil
		}
		return normalized, false, nil
	}
	if current, ok := c.apps[key]; ok {
		apply, err := shouldApplyEvent(current, normalized)
		if err != nil {
			return App{}, false, err
		}
		if !apply {
			return current, false, nil
		}
	}
	c.apps[key] = normalized
	rememberEventTime(c.lastEventAt, key, eventAt)
	c.markCacheUpdatedLocked(updatedAt)
	return normalized, true, nil
}

// Delete removes one app registration. Missing entries are treated as
// successful idempotent deletes. If app.UpdatedAt is older than the current
// cache entry, the stale delete is ignored.
func (c *Cache) Delete(app App, updatedAt time.Time) (Key, bool, error) {
	normalized, err := normalizeApp(app)
	if err != nil {
		return Key{}, false, err
	}
	eventAt, err := requireEventTime(normalized)
	if err != nil {
		return Key{}, false, err
	}
	key := keyFor(normalized)
	if c == nil {
		return key, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureMapsLocked()
	if watermark, ok := c.lastEventAt[key]; ok && eventAt.Before(watermark) {
		return key, false, nil
	}
	current, existed := c.apps[key]
	if existed {
		apply, err := shouldApplyEvent(current, normalized)
		if err != nil {
			return Key{}, false, err
		}
		if !apply {
			return key, false, nil
		}
	}
	delete(c.apps, key)
	rememberEventTime(c.lastEventAt, key, eventAt)
	c.markCacheUpdatedLocked(updatedAt)
	return key, existed, nil
}

// MarkLoadSkipped records that startup App Registry loading was not configured.
func (c *Cache) MarkLoadSkipped() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureMapsLocked()
	c.status = cacheStatusNotConfigured
	c.lastLoadErr = nil
}

// MarkLoadFailed records a failed load while preserving the previous snapshot.
func (c *Cache) MarkLoadFailed(err error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureMapsLocked()
	c.status = cacheStatusDegraded
	c.lastLoadErr = err
}

// List returns a stable copy of all cached apps.
func (c *Cache) List() []App {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	apps := make([]App, 0, len(c.apps))
	for _, app := range c.apps {
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Org != apps[j].Org {
			return apps[i].Org < apps[j].Org
		}
		if apps[i].Project != apps[j].Project {
			return apps[i].Project < apps[j].Project
		}
		return apps[i].Service < apps[j].Service
	})
	return apps
}

// Status returns the current cache status.
func (c *Cache) Status() Status {
	if c == nil {
		return Status{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	status := Status{
		AppsLoaded:    len(c.apps),
		LastLoadedAt:  c.lastLoaded,
		LastUpdatedAt: c.lastUpdated,
		State:         c.status,
	}
	if status.State == "" {
		status.State = cacheStatusNotConfigured
	}
	if c.lastLoadErr != nil {
		status.LastLoadError = c.lastLoadErr.Error()
	}
	status.IsDegraded = status.State == cacheStatusDegraded
	return status
}

func (c *Cache) markCacheUpdatedLocked(updatedAt time.Time) {
	c.lastUpdated = updatedAt.UTC()
	switch c.status {
	case "":
		c.status = cacheStatusNotConfigured
	case cacheStatusOK:
		c.lastLoadErr = nil
	}
}

func (c *Cache) ensureMapsLocked() {
	if c.apps == nil {
		c.apps = map[Key]App{}
	}
	if c.lastEventAt == nil {
		c.lastEventAt = map[Key]time.Time{}
	}
}

func requireEventTime(app App) (time.Time, error) {
	if app.UpdatedAt == "" {
		return time.Time{}, fmt.Errorf("updated_at is required")
	}
	eventAt, _, err := parseEventTime(app.UpdatedAt)
	return eventAt, err
}

func rememberEventTime(events map[Key]time.Time, key Key, eventAt time.Time) {
	if current, ok := events[key]; !ok || eventAt.After(current) {
		events[key] = eventAt.UTC()
	}
}

func shouldApplyEvent(current, incoming App) (bool, error) {
	incomingAt, hasIncomingAt, err := parseEventTime(incoming.UpdatedAt)
	if err != nil {
		return false, err
	}
	if !hasIncomingAt {
		return true, nil
	}
	currentAt, hasCurrentAt, err := parseEventTime(current.UpdatedAt)
	if err != nil {
		return false, fmt.Errorf("current cache updated_at is invalid: %w", err)
	}
	if !hasCurrentAt {
		return true, nil
	}
	return !incomingAt.Before(currentAt), nil
}

func parseEventTime(raw string) (time.Time, bool, error) {
	if raw == "" {
		return time.Time{}, false, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("updated_at must be RFC3339: %w", err)
	}
	return parsed, true, nil
}

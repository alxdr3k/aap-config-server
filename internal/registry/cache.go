package registry

import (
	"sort"
	"sync"
	"time"
)

// Cache stores the last Console App Registry snapshot in memory.
type Cache struct {
	mu          sync.RWMutex
	apps        map[Key]App
	lastLoaded  time.Time
	lastLoadErr error
}

// Status reports cache load state without exposing mutable cache internals.
type Status struct {
	AppsLoaded    int
	LastLoadedAt  time.Time
	LastLoadError string
}

// NewCache creates an empty registry cache.
func NewCache() *Cache {
	return &Cache{apps: map[Key]App{}}
}

// Replace atomically replaces the cached registry snapshot.
func (c *Cache) Replace(apps []App, loadedAt time.Time) {
	if c == nil {
		return
	}
	normalized := make(map[Key]App, len(apps))
	for _, app := range apps {
		next, err := normalizeApp(app)
		if err != nil {
			continue
		}
		normalized[keyFor(next)] = next
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apps = normalized
	c.lastLoaded = loadedAt.UTC()
	c.lastLoadErr = nil
}

// MarkLoadFailed records a failed load while preserving the previous snapshot.
func (c *Cache) MarkLoadFailed(err error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
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
		AppsLoaded:   len(c.apps),
		LastLoadedAt: c.lastLoaded,
	}
	if c.lastLoadErr != nil {
		status.LastLoadError = c.lastLoadErr.Error()
	}
	return status
}

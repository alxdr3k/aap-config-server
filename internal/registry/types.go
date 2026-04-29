package registry

import (
	"fmt"
	"sort"
	"time"
)

// App is one Console-owned service registration cached by Config Server.
type App struct {
	Org       string `json:"org"`
	Project   string `json:"project"`
	Service   string `json:"service,omitempty"`
	Name      string `json:"name,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Key uniquely identifies one registered app.
type Key struct {
	Org     string
	Project string
	Service string
}

func keyFor(app App) Key {
	return Key{Org: app.Org, Project: app.Project, Service: app.Service}
}

func normalizeApps(apps []App) ([]App, error) {
	normalized := make([]App, 0, len(apps))
	for i, app := range apps {
		next, err := normalizeApp(app)
		if err != nil {
			return nil, fmt.Errorf("app %d: %w", i, err)
		}
		normalized = append(normalized, next)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Org != normalized[j].Org {
			return normalized[i].Org < normalized[j].Org
		}
		if normalized[i].Project != normalized[j].Project {
			return normalized[i].Project < normalized[j].Project
		}
		return normalized[i].Service < normalized[j].Service
	})
	return normalized, nil
}

func normalizeApp(app App) (App, error) {
	if app.Service == "" {
		app.Service = app.Name
	}
	if app.Name == "" {
		app.Name = app.Service
	}
	if app.Org == "" {
		return App{}, fmt.Errorf("org is required")
	}
	if app.Project == "" {
		return App{}, fmt.Errorf("project is required")
	}
	if app.Service == "" {
		return App{}, fmt.Errorf("service or name is required")
	}
	if app.UpdatedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, app.UpdatedAt); err != nil {
			return App{}, fmt.Errorf("updated_at must be RFC3339: %w", err)
		}
	}
	return app, nil
}

package store

import (
	"fmt"
	"time"

	"github.com/aap/config-server/internal/parser"
)

// ServiceKey uniquely identifies a service.
type ServiceKey struct {
	Org     string
	Project string
	Service string
}

func (k ServiceKey) String() string {
	return k.Org + "/" + k.Project + "/" + k.Service
}

// configsPrefix is the path prefix for all service config files within the repo.
const configsPrefix = "configs"

// ServicePath returns the directory path for a service within the repo.
func ServicePath(org, project, service string) string {
	return fmt.Sprintf("%s/orgs/%s/projects/%s/services/%s", configsPrefix, org, project, service)
}

// ServiceData holds all in-memory config data for one service.
type ServiceData struct {
	Config    *parser.ServiceConfig
	EnvVars   *parser.EnvVarsConfig
	Secrets   *parser.SecretsConfig
	UpdatedAt time.Time
}

// ChangeRequest carries the payload for POST /admin/changes.
type ChangeRequest struct {
	Org     string
	Project string
	Service string
	// Config replaces config.yaml content (nil = no change).
	Config map[string]any
	// EnvVars replaces env_vars.yaml content (nil = no change).
	EnvVars *parser.EnvVars
	// Secrets will be handled in Phase 2.
	Message string
}

// ChangeResult is the response to a successful ChangeRequest.
type ChangeResult struct {
	Version   string
	UpdatedAt time.Time
	Files     []string // files that were written

	// ReloadFailed is set when the git commit/push succeeded but the in-memory
	// snapshot could not be refreshed from the new HEAD. Callers must treat
	// this as "committed but stale read" and not as plain success.
	ReloadFailed bool
	ReloadError  string
}

// DeleteRequest carries the payload for DELETE /admin/changes.
type DeleteRequest struct {
	Org     string
	Project string
	Service string
}

// DeleteResult is the response to a successful DeleteRequest.
type DeleteResult struct {
	Version      string
	UpdatedAt    time.Time
	DeletedFiles []string
}

// ServiceInfo is a summary entry returned by the services listing API.
type ServiceInfo struct {
	Name       string
	HasConfig  bool
	HasEnvVars bool
	HasSecrets bool
	UpdatedAt  time.Time
}

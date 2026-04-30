package store

import (
	"fmt"
	"time"

	"github.com/aap/config-server/internal/parser"
	"github.com/aap/config-server/internal/secret"
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

	ConfigResourceVersion  string
	EnvVarsResourceVersion string

	configDigest  string
	envVarsDigest string
}

// ChangeRequest carries the payload for POST /api/v1/admin/changes.
type ChangeRequest struct {
	Org     string
	Project string
	Service string
	// Config replaces config.yaml content (nil = no change).
	Config map[string]any
	// EnvVars replaces env_vars.yaml content (nil = no change).
	EnvVars *parser.EnvVars
	// Secrets carries plaintext secret writes grouped by K8s Secret name.
	Secrets map[string]SecretWrite
	Message string
}

// SecretWrite carries plaintext values for one K8s Secret object. Values must
// not be logged and are converted to SealedSecret manifests before Git writes.
type SecretWrite struct {
	Namespace string
	Data      map[string]secret.Value
}

// ChangeResult is the response to a successful ChangeRequest.
type ChangeResult struct {
	Version   string
	UpdatedAt time.Time
	Files     []string // files that were written

	// ApplyFailed is set when the git commit/push succeeded but applying the
	// SealedSecret manifest(s) to Kubernetes failed.
	ApplyFailed bool
	ApplyError  string

	// ReloadFailed is set when the git commit/push succeeded but the in-memory
	// snapshot could not be refreshed from the new HEAD. Callers must treat
	// this as "committed but stale read" and not as plain success.
	ReloadFailed bool
	ReloadError  string
}

// DeleteRequest carries the payload for DELETE /api/v1/admin/changes.
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

	// ReloadFailed is set when the git delete/push succeeded but the in-memory
	// snapshot could not be refreshed from the new HEAD. The last-known-good
	// snapshot stays in place; callers must treat this as "deleted but stale read".
	ReloadFailed bool
	ReloadError  string
}

// HistoryOptions controls service history listing.
type HistoryOptions struct {
	Org     string
	Project string
	Service string
	File    string
	Limit   int
	Before  string
}

// HistoryEntry is one service-scoped Git commit exposed by the history API.
type HistoryEntry struct {
	Version      string
	Message      string
	Author       string
	Timestamp    time.Time
	FilesChanged []string
}

// StoreStatus holds operational status information about the store.
type StoreStatus struct {
	Version         string
	ServicesLoaded  int
	LastReloadAt    time.Time
	IsDegraded      bool
	LastReloadError string
}

// ServiceInfo is a summary entry returned by the services listing API.
type ServiceInfo struct {
	Name       string
	HasConfig  bool
	HasEnvVars bool
	HasSecrets bool
	UpdatedAt  time.Time
}

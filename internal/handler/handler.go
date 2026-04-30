package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aap/config-server/internal/apperror"
	"github.com/aap/config-server/internal/parser"
	"github.com/aap/config-server/internal/registry"
	"github.com/aap/config-server/internal/secret"
	"github.com/aap/config-server/internal/store"
)

// ConfigStore is the interface the handlers need from the store.
type ConfigStore interface {
	GetConfig(ctx context.Context, org, project, service string) (*store.ServiceData, error)
	GetConfigAtVersion(ctx context.Context, org, project, service, version string) (*store.ServiceData, error)
	GetEnvVarsAtVersion(ctx context.Context, org, project, service, version string) (*store.ServiceData, error)
	History(ctx context.Context, opts store.HistoryOptions) ([]store.HistoryEntry, error)
	ResourceVersion(ctx context.Context, org, project, service, resource string) (string, string, error)
	WaitForVersionChange(ctx context.Context, version string) (string, bool, error)
	ListOrgs() []string
	ListProjects(org string) []string
	ListServices(org, project string) []store.ServiceInfo
	ApplyChanges(ctx context.Context, req *store.ChangeRequest) (*store.ChangeResult, error)
	DeleteChanges(ctx context.Context, req *store.DeleteRequest) (*store.DeleteResult, error)
	HeadVersion() string
	RefreshFromRepo(ctx context.Context) (bool, error)
	ReloadFromRepo(ctx context.Context) (bool, error)
	IsDegraded() bool
	StatusInfo() store.StoreStatus
}

// Readiness is used to query whether the server is ready.
type Readiness interface {
	IsReady() bool
}

// Handler groups all HTTP handlers together.
type Handler struct {
	store       ConfigStore
	readiness   Readiness
	apiKey      string
	appRegistry *registry.Cache
	secretDeps  secret.Dependencies
}

const (
	defaultWatchTimeout = 30 * time.Second
	maxWatchTimeout     = 30 * time.Second
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
)

// Option customizes Handler dependencies.
type Option func(*Handler)

// WithSecretDependencies wires secret read dependencies into handlers that
// resolve secret-backed env vars.
func WithSecretDependencies(deps secret.Dependencies) Option {
	return func(h *Handler) {
		h.secretDeps = deps.WithDefaults()
	}
}

// WithAppRegistry wires the Console-owned App Registry cache for future
// registry webhook/status endpoints.
func WithAppRegistry(cache *registry.Cache) Option {
	return func(h *Handler) {
		h.appRegistry = cache
	}
}

// New creates a Handler. If apiKey is empty, authenticated endpoints are left
// open — this is intended only for tests; production wiring must pass a key
// (enforced by config.Validate).
func New(st ConfigStore, ready Readiness, apiKey string, opts ...Option) *Handler {
	h := &Handler{store: st, readiness: ready, apiKey: apiKey}
	for _, opt := range opts {
		opt(h)
	}
	h.secretDeps = h.secretDeps.WithDefaults()
	if h.appRegistry == nil {
		h.appRegistry = registry.NewCache()
	}
	return h
}

// Routes registers all routes on the given mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	// Health & ops
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("GET /api/v1/status", h.status)

	// Service discovery
	mux.HandleFunc("GET /api/v1/orgs", h.listOrgs)
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects", h.listProjects)
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services", h.listServices)

	// Config read
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config",
		h.getConfig)
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config/watch",
		h.watchConfig)
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars",
		h.getEnvVars)
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars/watch",
		h.watchEnvVars)
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/history",
		h.getHistory)
	// Secret metadata is privileged even though values are never returned; auth
	// is required so unauthenticated callers cannot enumerate which K8s secret
	// objects back a service.
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/secrets",
		h.requireKey(h.getSecrets))

	// Admin write — protected by API key
	mux.HandleFunc("POST /api/v1/admin/changes", h.requireKey(h.postChanges))
	mux.HandleFunc("DELETE /api/v1/admin/changes", h.requireKey(h.deleteChanges))
	mux.HandleFunc("POST /api/v1/admin/reload", h.requireKey(h.adminReload))
	mux.HandleFunc("POST /api/v1/admin/app-registry/webhook", h.requireKey(h.appRegistryWebhook))
}

// ---- health ----

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) readyz(w http.ResponseWriter, _ *http.Request) {
	if h.readiness != nil && !h.readiness.IsReady() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	if h.store.IsDegraded() {
		http.Error(w, "degraded", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	si := h.store.StatusInfo()
	registryStatus := h.appRegistry.Status()
	degradedComponents := make([]string, 0, 2)
	resp := map[string]any{
		"status":          "ok",
		"version":         si.Version,
		"services_loaded": si.ServicesLoaded,
		"app_registry":    registryStatusBody(registryStatus),
	}
	if !si.LastReloadAt.IsZero() {
		resp["last_reload_at"] = si.LastReloadAt.UTC().Format(time.RFC3339)
	}
	if si.IsDegraded {
		degradedComponents = append(degradedComponents, "store")
		resp["last_reload_error"] = si.LastReloadError
	}
	if registryStatus.IsDegraded {
		degradedComponents = append(degradedComponents, "app_registry")
	}
	if len(degradedComponents) > 0 {
		resp["status"] = "degraded"
		resp["is_degraded"] = true
		resp["degraded_components"] = degradedComponents
	}
	respondJSON(w, http.StatusOK, resp)
}

func registryStatusBody(status registry.Status) map[string]any {
	state := status.State
	if state == "" {
		state = "not_configured"
	}
	body := map[string]any{
		"status":      state,
		"apps_loaded": status.AppsLoaded,
	}
	if !status.LastLoadedAt.IsZero() {
		body["last_loaded_at"] = status.LastLoadedAt.UTC().Format(time.RFC3339)
	}
	if !status.LastUpdatedAt.IsZero() {
		body["last_updated_at"] = status.LastUpdatedAt.UTC().Format(time.RFC3339)
	}
	if status.LastLoadError != "" {
		body["last_load_error"] = status.LastLoadError
	}
	return body
}

// adminReload force-reloads the store from the repo. It pulls any remote
// changes and then re-parses the current checkout unconditionally, so that a
// store which is degraded from an earlier parse failure recovers even when
// HEAD has not moved since. The background poll path uses the lazier
// RefreshFromRepo (skip reload if HEAD unchanged) — an operator-triggered
// reload is the stronger signal and must not silently return 200 while the
// store is still serving a last-known-good snapshot.
func (h *Handler) adminReload(w http.ResponseWriter, r *http.Request) {
	updated, err := h.store.ReloadFromRepo(r.Context())
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":       "reload_failed",
			"reload_error": err.Error(),
			"version":      h.store.HeadVersion(),
		})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"updated": updated,
		"version": h.store.HeadVersion(),
	})
}

type appRegistryWebhookRequest struct {
	Action    string        `json:"action"`
	App       *registry.App `json:"app"`
	Org       string        `json:"org"`
	Project   string        `json:"project"`
	Service   string        `json:"service"`
	Name      string        `json:"name"`
	UpdatedAt string        `json:"updated_at"`
}

func (h *Handler) appRegistryWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body appRegistryWebhookRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_body", h.explainDecodeError(err))
		return
	}

	action := strings.ToLower(strings.TrimSpace(body.Action))
	app := body.registryApp()
	now := time.Now().UTC()
	switch action {
	case "create", "update", "upsert":
		if _, _, err := h.appRegistry.Upsert(app, now); err != nil {
			respondErrorCode(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
	case "delete":
		if _, _, err := h.appRegistry.Delete(app, now); err != nil {
			respondErrorCode(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
	default:
		respondErrorCode(w, http.StatusBadRequest, "validation",
			"action must be one of create, update, upsert, or delete")
		return
	}

	status := h.appRegistry.Status()
	respondJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"action":      action,
		"apps_loaded": status.AppsLoaded,
	})
}

func (r appRegistryWebhookRequest) registryApp() registry.App {
	app := registry.App{}
	if r.App != nil {
		app = *r.App
	}
	if app.Org == "" {
		app.Org = r.Org
	}
	if app.Project == "" {
		app.Project = r.Project
	}
	if app.Service == "" {
		app.Service = r.Service
	}
	if app.Name == "" {
		app.Name = r.Name
	}
	if app.UpdatedAt == "" {
		app.UpdatedAt = r.UpdatedAt
	}
	return app
}

// ---- service discovery ----

func (h *Handler) listOrgs(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"orgs": h.store.ListOrgs(),
	})
}

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	respondJSON(w, http.StatusOK, map[string]any{
		"org":      org,
		"projects": h.store.ListProjects(org),
	})
}

func (h *Handler) listServices(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	project := r.PathValue("project")
	svcs := h.store.ListServices(org, project)

	type svcEntry struct {
		Name       string    `json:"name"`
		HasConfig  bool      `json:"has_config"`
		HasEnvVars bool      `json:"has_env_vars"`
		HasSecrets bool      `json:"has_secrets"`
		UpdatedAt  time.Time `json:"updated_at"`
	}
	entries := make([]svcEntry, len(svcs))
	for i, s := range svcs {
		entries[i] = svcEntry{
			Name:       s.Name,
			HasConfig:  s.HasConfig,
			HasEnvVars: s.HasEnvVars,
			HasSecrets: s.HasSecrets,
			UpdatedAt:  s.UpdatedAt,
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"org":      org,
		"project":  project,
		"services": entries,
	})
}

// ---- config read ----

func (h *Handler) getConfig(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	project := r.PathValue("project")
	service := r.PathValue("service")
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	if version != "" {
		h.writeConfigAtVersionResponse(w, r.Context(), org, project, service, version)
		return
	}

	h.writeConfigResponse(w, r.Context(), org, project, service)
}

func (h *Handler) watchConfig(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	project := r.PathValue("project")
	service := r.PathValue("service")

	if !h.waitForWatchChange(w, r, org, project, service, "config") {
		return
	}

	h.writeConfigResponse(w, r.Context(), org, project, service)
}

func (h *Handler) watchEnvVars(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	project := r.PathValue("project")
	service := r.PathValue("service")

	if !h.waitForWatchChange(w, r, org, project, service, "env_vars") {
		return
	}

	h.writeEnvVarsResponse(w, r.Context(), org, project, service, false)
}

func (h *Handler) waitForWatchChange(
	w http.ResponseWriter,
	r *http.Request,
	org, project, service, resource string,
) bool {
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	if version == "" {
		respondErrorCode(w, http.StatusBadRequest, "invalid_query", "version query parameter is required")
		return false
	}
	timeout, err := parseWatchTimeout(r)
	if err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_query", err.Error())
		return false
	}

	waitCtx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	for {
		if waitCtx.Err() != nil {
			w.WriteHeader(http.StatusNotModified)
			return false
		}
		resourceVersion, headVersion, err := h.store.ResourceVersion(r.Context(), org, project, service, resource)
		if err != nil {
			respondError(w, err)
			return false
		}
		if resourceVersion != version {
			return true
		}

		_, changed, err := h.store.WaitForVersionChange(waitCtx, headVersion)
		if err != nil {
			if !changed && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
				w.WriteHeader(http.StatusNotModified)
				return false
			}
			slog.Error("watch failed", "resource", resource, "err", err)
			respondErrorCode(w, http.StatusInternalServerError, "internal", "internal server error")
			return false
		}
		if !changed {
			w.WriteHeader(http.StatusNotModified)
			return false
		}
	}
}

func (h *Handler) writeConfigResponse(w http.ResponseWriter, ctx context.Context, org, project, service string) {
	d, err := h.store.GetConfig(ctx, org, project, service)
	if err != nil {
		respondError(w, err)
		return
	}

	version := d.ConfigResourceVersion
	if version == "" {
		version = h.store.HeadVersion()
	}
	meta := configMeta(org, project, service, version, d.UpdatedAt)

	if d.Config == nil {
		respondJSON(w, http.StatusOK, map[string]any{
			"metadata": meta,
			"config":   map[string]any{},
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"metadata": meta,
		"config":   d.Config.Config,
	})
}

func (h *Handler) writeConfigAtVersionResponse(
	w http.ResponseWriter,
	ctx context.Context,
	org, project, service, version string,
) {
	d, err := h.store.GetConfigAtVersion(ctx, org, project, service, version)
	if err != nil {
		respondError(w, err)
		return
	}
	meta := configMeta(org, project, service, version, d.UpdatedAt)
	config := map[string]any{}
	if d.Config != nil && d.Config.Config != nil {
		config = d.Config.Config
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"metadata": meta,
		"config":   config,
	})
}

func (h *Handler) getEnvVars(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	project := r.PathValue("project")
	service := r.PathValue("service")
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	resolveSecrets, err := parseBoolQuery(r, "resolve_secrets")
	if err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	if version != "" && resolveSecrets {
		respondErrorCode(w, http.StatusBadRequest, "invalid_query", "version cannot be combined with resolve_secrets")
		return
	}
	if resolveSecrets {
		if !h.authenticate(w, r) {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Del("ETag")
	}
	if version != "" {
		h.writeEnvVarsAtVersionResponse(w, r.Context(), org, project, service, version)
		return
	}

	h.writeEnvVarsResponse(w, r.Context(), org, project, service, resolveSecrets)
}

func (h *Handler) writeEnvVarsResponse(
	w http.ResponseWriter,
	ctx context.Context,
	org, project, service string,
	resolveSecrets bool,
) {
	d, err := h.store.GetConfig(ctx, org, project, service)
	if err != nil {
		respondError(w, err)
		return
	}

	version := h.store.HeadVersion()
	if !resolveSecrets && d.EnvVarsResourceVersion != "" {
		version = d.EnvVarsResourceVersion
	}
	if version == "" {
		version = h.store.HeadVersion()
	}
	meta := configMeta(org, project, service, version, d.UpdatedAt)

	if d.EnvVars == nil {
		if resolveSecrets {
			respondJSON(w, http.StatusOK, map[string]any{
				"metadata": meta,
				"env_vars": map[string]any{
					"plain":   map[string]string{},
					"secrets": map[string]string{},
				},
			})
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"metadata": meta,
			"env_vars": map[string]any{
				"plain":       map[string]string{},
				"secret_refs": map[string]string{},
			},
		})
		return
	}

	if resolveSecrets {
		resolved, err := h.resolveEnvSecrets(ctx, org, project, service, d)
		if err != nil {
			respondError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"metadata": meta,
			"env_vars": map[string]any{
				"plain":   nullToEmpty(d.EnvVars.EnvVars.Plain),
				"secrets": resolved,
			},
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"metadata": meta,
		"env_vars": map[string]any{
			"plain":       nullToEmpty(d.EnvVars.EnvVars.Plain),
			"secret_refs": nullToEmpty(d.EnvVars.EnvVars.SecretRefs),
		},
	})
}

func (h *Handler) writeEnvVarsAtVersionResponse(
	w http.ResponseWriter,
	ctx context.Context,
	org, project, service, version string,
) {
	d, err := h.store.GetEnvVarsAtVersion(ctx, org, project, service, version)
	if err != nil {
		respondError(w, err)
		return
	}
	meta := configMeta(org, project, service, version, d.UpdatedAt)
	envVars := map[string]any{
		"plain":       map[string]string{},
		"secret_refs": map[string]string{},
	}
	if d.EnvVars != nil {
		envVars["plain"] = nullToEmpty(d.EnvVars.EnvVars.Plain)
		envVars["secret_refs"] = nullToEmpty(d.EnvVars.EnvVars.SecretRefs)
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"metadata": meta,
		"env_vars": envVars,
	})
}

func (h *Handler) resolveEnvSecrets(ctx context.Context, org, project, service string, d *store.ServiceData) (map[string]string, error) {
	refs := d.EnvVars.EnvVars.SecretRefs
	auditResult := "success"
	auditSecretIDs := secretRefAuditIDs(refs)
	defer func() {
		h.recordSecretAudit(context.WithoutCancel(ctx), secret.AuditEvent{
			Action:    "secret_env_resolve",
			Result:    auditResult,
			Org:       org,
			Project:   project,
			Service:   service,
			SecretIDs: auditSecretIDs,
		})
	}()
	if len(refs) == 0 {
		return map[string]string{}, nil
	}
	if h.secretDeps.VolumeReader == nil {
		auditResult = "configuration_error"
		return nil, apperror.New(apperror.CodeInternal, "secret volume reader is not configured")
	}
	if d.Secrets == nil {
		auditResult = "metadata_missing"
		return nil, apperror.New(apperror.CodeInternal, "secret metadata is required to resolve env var secret_refs")
	}

	byID := make(map[string]parser.SecretEntry, len(d.Secrets.Secrets))
	for _, entry := range d.Secrets.Secrets {
		if _, exists := byID[entry.ID]; exists {
			auditResult = "duplicate_secret_id"
			return nil, apperror.New(apperror.CodeInternal,
				"secret metadata contains duplicate id")
		}
		byID[entry.ID] = entry
	}

	resolved := make(map[string]string, len(refs))
	for envName, secretID := range refs {
		entry, ok := byID[secretID]
		if !ok {
			auditResult = "unknown_secret_id"
			return nil, apperror.New(apperror.CodeInternal,
				"env var secret_ref references unknown secret metadata id")
		}
		value, err := h.secretDeps.VolumeReader.Refresh(ctx, secret.Reference{
			ID:        entry.ID,
			Namespace: entry.K8sSecret.Namespace,
			Name:      entry.K8sSecret.Name,
			Key:       entry.K8sSecret.Key,
		})
		if err != nil {
			auditResult = "read_failed"
			return nil, apperror.Wrap(apperror.CodeInternal, "read mounted secret value", err)
		}
		bytes := value.Bytes()
		resolved[envName] = string(bytes)
		value.Destroy()
		for i := range bytes {
			bytes[i] = 0
		}
	}
	return resolved, nil
}

func (h *Handler) getSecrets(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	project := r.PathValue("project")
	service := r.PathValue("service")

	d, err := h.store.GetConfig(r.Context(), org, project, service)
	if err != nil {
		respondError(w, err)
		return
	}

	meta := configMeta(org, project, service, h.store.HeadVersion(), d.UpdatedAt)

	if d.Secrets == nil {
		respondJSON(w, http.StatusOK, map[string]any{
			"metadata": meta,
			"secrets":  []any{},
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"metadata": meta,
		"secrets":  d.Secrets.Secrets,
	})
}

func (h *Handler) getHistory(w http.ResponseWriter, r *http.Request) {
	opts, err := parseHistoryOptions(r)
	if err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	opts.Org = r.PathValue("org")
	opts.Project = r.PathValue("project")
	opts.Service = r.PathValue("service")

	entries, err := h.store.History(r.Context(), opts)
	if err != nil {
		respondError(w, err)
		return
	}

	type historyEntryBody struct {
		Version      string   `json:"version"`
		Message      string   `json:"message"`
		Author       string   `json:"author"`
		Timestamp    string   `json:"timestamp"`
		FilesChanged []string `json:"files_changed"`
	}

	history := make([]historyEntryBody, len(entries))
	for i, entry := range entries {
		history[i] = historyEntryBody{
			Version:      entry.Version,
			Message:      entry.Message,
			Author:       entry.Author,
			Timestamp:    entry.Timestamp.UTC().Format(time.RFC3339),
			FilesChanged: entry.FilesChanged,
		}
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"metadata": map[string]string{
			"org":     opts.Org,
			"project": opts.Project,
			"service": opts.Service,
		},
		"history": history,
	})
}

// ---- admin write ----

// postChangesRequest matches the POST /api/v1/admin/changes payload.
type postChangesRequest struct {
	Org     string                     `json:"org"`
	Project string                     `json:"project"`
	Service string                     `json:"service"`
	Config  map[string]any             `json:"config"`
	EnvVars *envVarsBody               `json:"env_vars"`
	Secrets map[string]secretWriteBody `json:"secrets"`
	Message string                     `json:"message"`
}

type envVarsBody struct {
	Plain      map[string]string `json:"plain"`
	SecretRefs map[string]string `json:"secret_refs"`
}

type secretWriteBody struct {
	Namespace string            `json:"namespace"`
	Data      map[string]string `json:"data"`
}

func (h *Handler) postChanges(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body postChangesRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_body", h.explainDecodeError(err))
		return
	}

	req := &store.ChangeRequest{
		Org:     body.Org,
		Project: body.Project,
		Service: body.Service,
		Config:  body.Config,
		Message: body.Message,
	}
	if body.EnvVars != nil {
		req.EnvVars = &parser.EnvVars{
			Plain:      body.EnvVars.Plain,
			SecretRefs: body.EnvVars.SecretRefs,
		}
	}
	if body.Secrets != nil {
		req.Secrets = make(map[string]store.SecretWrite, len(body.Secrets))
		for name, write := range body.Secrets {
			data := make(map[string]secret.Value, len(write.Data))
			for key, value := range write.Data {
				data[key] = secret.NewValue([]byte(value))
			}
			req.Secrets[name] = store.SecretWrite{
				Namespace: write.Namespace,
				Data:      data,
			}
		}
		defer destroySecretWrites(req.Secrets)
	}

	result, err := h.store.ApplyChanges(r.Context(), req)
	if err != nil {
		respondError(w, err)
		return
	}

	status := "committed"
	code := http.StatusOK
	switch {
	case result.ApplyFailed && result.ReloadFailed:
		status = "committed_but_apply_and_reload_failed"
		code = http.StatusServiceUnavailable
	case result.ApplyFailed:
		status = "committed_but_apply_failed"
		code = http.StatusServiceUnavailable
	case result.ReloadFailed:
		// Git write succeeded but the serving snapshot could not be refreshed
		// from the new HEAD. We surface this explicitly so operators can react
		// instead of trusting a plain 200.
		status = "committed_but_reload_failed"
		code = http.StatusServiceUnavailable
	}

	resp := map[string]any{
		"status":     status,
		"version":    result.Version,
		"updated_at": result.UpdatedAt,
		"files":      result.Files,
	}
	if result.ReloadFailed && result.ReloadError != "" {
		resp["reload_error"] = result.ReloadError
	}
	if result.ApplyFailed && result.ApplyError != "" {
		resp["apply_error"] = result.ApplyError
	}
	respondJSON(w, code, resp)
}

// explainDecodeError gives a slightly friendlier hint for unknown fields.
func (h *Handler) explainDecodeError(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "unknown field") {
		return "request contains an unknown field: " + msg
	}
	return "invalid JSON body: " + msg
}

type deleteChangesRequest struct {
	Org     string `json:"org"`
	Project string `json:"project"`
	Service string `json:"service"`
}

func (h *Handler) deleteChanges(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body deleteChangesRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_body", "invalid JSON body: "+err.Error())
		return
	}

	result, err := h.store.DeleteChanges(r.Context(), &store.DeleteRequest{
		Org:     body.Org,
		Project: body.Project,
		Service: body.Service,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	status := "deleted"
	code := http.StatusOK
	if result.ReloadFailed {
		status = "deleted_but_reload_failed"
		code = http.StatusServiceUnavailable
	}

	resp := map[string]any{
		"status":        status,
		"version":       result.Version,
		"deleted_files": result.DeletedFiles,
	}
	if result.ReloadFailed && result.ReloadError != "" {
		resp["reload_error"] = result.ReloadError
	}
	respondJSON(w, code, resp)
}

// ---- helpers ----

// requireKey enforces API key authentication. Accepts `Authorization: Bearer
// <key>` (canonical) or the legacy `X-API-Key` header. If apiKey is empty, the
// middleware allows the request through — this is only reachable in tests;
// production startup requires a key (or an explicit dev opt-in flag).
func (h *Handler) requireKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.authenticate(w, r) {
			return
		}
		next(w, r)
	}
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) bool {
	if h.apiKey == "" {
		return true
	}
	if !authorized(r, h.apiKey) {
		respondErrorCode(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API key")
		return false
	}
	return true
}

// authorized reports whether r presents a valid credential matching key. The
// comparison uses crypto/subtle.ConstantTimeCompare to avoid timing side
// channels.
//
// The Authorization header is parsed per RFC 7235: the auth-scheme token is
// case-insensitive, so "Bearer", "bearer", and "BEARER" are all accepted.
func authorized(r *http.Request, key string) bool {
	if v := r.Header.Get("Authorization"); v != "" {
		scheme, token, hasSep := strings.Cut(v, " ")
		if hasSep && strings.EqualFold(scheme, "bearer") {
			return constantTimeEqual(token, key)
		}
	}
	if v := r.Header.Get("X-API-Key"); v != "" {
		return constantTimeEqual(v, key)
	}
	return false
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		// Still do a constant-time compare of equal-length strings so timing
		// doesn't leak whether the length matched; the length itself leaks but
		// length alone is not meaningful for a fixed-length key deployment.
		_ = subtle.ConstantTimeCompare([]byte(a), []byte(a))
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func configMeta(org, project, service, version string, updatedAt time.Time) map[string]any {
	return map[string]any{
		"org":        org,
		"project":    project,
		"service":    service,
		"version":    version,
		"updated_at": updatedAt.Format(time.RFC3339),
	}
}

func parseBoolQuery(r *http.Request, name string) (bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, errors.New(name + " must be a boolean")
	}
	return value, nil
}

func parseWatchTimeout(r *http.Request) (time.Duration, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("timeout"))
	if raw == "" {
		return defaultWatchTimeout, nil
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil {
		return 0, errors.New("timeout must be a duration like 30s")
	}
	if timeout <= 0 {
		return 0, errors.New("timeout must be greater than 0")
	}
	if timeout > maxWatchTimeout {
		return 0, errors.New("timeout must be less than or equal to 30s")
	}
	return timeout, nil
}

func parseHistoryOptions(r *http.Request) (store.HistoryOptions, error) {
	opts := store.HistoryOptions{
		File:   strings.TrimSpace(r.URL.Query().Get("file")),
		Before: strings.TrimSpace(r.URL.Query().Get("before")),
		Limit:  defaultHistoryLimit,
	}
	switch opts.File {
	case "", "config", "env_vars", "secrets":
	default:
		return opts, errors.New("file must be one of config, env_vars, or secrets")
	}

	rawLimit := strings.TrimSpace(r.URL.Query().Get("limit"))
	if rawLimit == "" {
		return opts, nil
	}
	limit, err := strconv.Atoi(rawLimit)
	if err != nil {
		return opts, errors.New("limit must be an integer")
	}
	if limit <= 0 {
		return opts, errors.New("limit must be greater than 0")
	}
	if limit > maxHistoryLimit {
		return opts, errors.New("limit must be less than or equal to 100")
	}
	opts.Limit = limit
	return opts, nil
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "err", err)
	}
}

// errorBody is the standard JSON error envelope.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func respondErrorCode(w http.ResponseWriter, status int, code, message string) {
	respondJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

func respondError(w http.ResponseWriter, err error) {
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case apperror.CodeNotFound:
			respondErrorCode(w, http.StatusNotFound, "not_found", appErr.Message)
		case apperror.CodeValidation:
			respondErrorCode(w, http.StatusBadRequest, "validation", appErr.Message)
		case apperror.CodeConflict:
			respondErrorCode(w, http.StatusConflict, "conflict", appErr.Message)
		case apperror.CodeUnauthorized:
			respondErrorCode(w, http.StatusUnauthorized, "unauthorized", appErr.Message)
		case apperror.CodeGitPush:
			respondErrorCode(w, http.StatusServiceUnavailable, "git_push_failed", appErr.Message)
		default:
			respondErrorCode(w, http.StatusInternalServerError, "internal", "internal server error")
		}
		return
	}
	slog.Error("unhandled error", "err", err)
	respondErrorCode(w, http.StatusInternalServerError, "internal", "internal server error")
}

func nullToEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func secretRefAuditIDs(refs map[string]string) []string {
	seen := make(map[string]struct{}, len(refs))
	for _, id := range refs {
		if id == "" {
			continue
		}
		seen[id] = struct{}{}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func destroySecretWrites(writes map[string]store.SecretWrite) {
	for _, write := range writes {
		for key, value := range write.Data {
			value.Destroy()
			write.Data[key] = value
		}
	}
}

func (h *Handler) recordSecretAudit(ctx context.Context, event secret.AuditEvent) {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if h.secretDeps.Auditor == nil {
		return
	}
	if err := h.secretDeps.Auditor.Record(ctx, event); err != nil {
		slog.Warn("record secret audit event failed",
			"err", err,
			"action", event.Action,
			"result", event.Result,
			"org", event.Org,
			"project", event.Project,
			"service", event.Service,
			"secret_ids", event.SecretIDs)
	}
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/aap/config-server/internal/apperror"
	"github.com/aap/config-server/internal/parser"
	"github.com/aap/config-server/internal/store"
)

// ConfigStore is the interface the handlers need from the store.
type ConfigStore interface {
	GetConfig(ctx context.Context, org, project, service string) (*store.ServiceData, error)
	ListOrgs() []string
	ListProjects(org string) []string
	ListServices(org, project string) []store.ServiceInfo
	ApplyChanges(ctx context.Context, req *store.ChangeRequest) (*store.ChangeResult, error)
	DeleteChanges(ctx context.Context, req *store.DeleteRequest) (*store.DeleteResult, error)
	HeadVersion() string
	RefreshFromRepo(ctx context.Context) (bool, error)
}

// Readiness is used to query whether the server is ready.
type Readiness interface {
	IsReady() bool
}

// Handler groups all HTTP handlers together.
type Handler struct {
	store    ConfigStore
	readiness Readiness
}

// New creates a Handler.
func New(st ConfigStore, ready Readiness) *Handler {
	return &Handler{store: st, readiness: ready}
}

// Routes registers all routes on the given mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	// Health & ops
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("GET /api/v1/status", h.status)
	mux.HandleFunc("POST /api/v1/admin/reload", h.adminReload)

	// Service discovery
	mux.HandleFunc("GET /api/v1/orgs", h.listOrgs)
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects", h.listProjects)
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services", h.listServices)

	// Config read
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config",
		h.getConfig)
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars",
		h.getEnvVars)
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/secrets",
		h.getSecrets)

	// Admin write
	mux.HandleFunc("POST /api/v1/admin/changes", h.postChanges)
	mux.HandleFunc("DELETE /api/v1/admin/changes", h.deleteChanges)
}

// ---- health ----

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (h *Handler) readyz(w http.ResponseWriter, _ *http.Request) {
	if h.readiness != nil && !h.readiness.IsReady() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": h.store.HeadVersion(),
	})
}

func (h *Handler) adminReload(w http.ResponseWriter, r *http.Request) {
	updated, err := h.store.RefreshFromRepo(r.Context())
	if err != nil {
		respondError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"updated": updated,
		"version": h.store.HeadVersion(),
	})
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

	d, err := h.store.GetConfig(r.Context(), org, project, service)
	if err != nil {
		respondError(w, err)
		return
	}

	meta := configMeta(org, project, service, h.store.HeadVersion())

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

func (h *Handler) getEnvVars(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	project := r.PathValue("project")
	service := r.PathValue("service")

	d, err := h.store.GetConfig(r.Context(), org, project, service)
	if err != nil {
		respondError(w, err)
		return
	}

	meta := configMeta(org, project, service, h.store.HeadVersion())

	if d.EnvVars == nil {
		respondJSON(w, http.StatusOK, map[string]any{
			"metadata": meta,
			"env_vars": map[string]any{
				"plain":       map[string]string{},
				"secret_refs": map[string]string{},
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

func (h *Handler) getSecrets(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	project := r.PathValue("project")
	service := r.PathValue("service")

	d, err := h.store.GetConfig(r.Context(), org, project, service)
	if err != nil {
		respondError(w, err)
		return
	}

	meta := configMeta(org, project, service, h.store.HeadVersion())

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

// ---- admin write ----

type postChangesRequest struct {
	Org     string         `json:"org"`
	Project string         `json:"project"`
	Service string         `json:"service"`
	Config  map[string]any `json:"config"`
	EnvVars *envVarsBody   `json:"env_vars"`
	Message string         `json:"message"`
}

type envVarsBody struct {
	Plain      map[string]string `json:"plain"`
	SecretRefs map[string]string `json:"secret_refs"`
}

func (h *Handler) postChanges(w http.ResponseWriter, r *http.Request) {
	var body postChangesRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
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

	result, err := h.store.ApplyChanges(r.Context(), req)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"status":     "committed",
		"version":    result.Version,
		"updated_at": result.UpdatedAt,
		"files":      result.Files,
	})
}

type deleteChangesRequest struct {
	Org     string `json:"org"`
	Project string `json:"project"`
	Service string `json:"service"`
}

func (h *Handler) deleteChanges(w http.ResponseWriter, r *http.Request) {
	var body deleteChangesRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
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

	respondJSON(w, http.StatusOK, map[string]any{
		"status":        "deleted",
		"version":       result.Version,
		"deleted_files": result.DeletedFiles,
	})
}

// ---- helpers ----

func configMeta(org, project, service, version string) map[string]any {
	return map[string]any{
		"org":     org,
		"project": project,
		"service": service,
		"version": version,
	}
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "err", err)
	}
}

func respondError(w http.ResponseWriter, err error) {
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case apperror.CodeNotFound:
			http.Error(w, appErr.Message, http.StatusNotFound)
		case apperror.CodeValidation:
			http.Error(w, appErr.Message, http.StatusBadRequest)
		case apperror.CodeConflict:
			http.Error(w, appErr.Message, http.StatusConflict)
		case apperror.CodeUnauthorized:
			http.Error(w, appErr.Message, http.StatusUnauthorized)
		case apperror.CodeGitPush:
			http.Error(w, appErr.Message, http.StatusServiceUnavailable)
		default:
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}
	slog.Error("unhandled error", "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func nullToEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

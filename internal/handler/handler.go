package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
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
	store     ConfigStore
	readiness Readiness
	apiKey    string
}

// New creates a Handler. If apiKey is empty, authenticated endpoints are left
// open — this is intended only for tests; production wiring must pass a key
// (enforced by config.Validate).
func New(st ConfigStore, ready Readiness, apiKey string) *Handler {
	return &Handler{store: st, readiness: ready, apiKey: apiKey}
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
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars",
		h.getEnvVars)
	// Secret metadata is privileged even though values are never returned; auth
	// is required so unauthenticated callers cannot enumerate which K8s secret
	// objects back a service.
	mux.HandleFunc("GET /api/v1/orgs/{org}/projects/{project}/services/{service}/secrets",
		h.requireKey(h.getSecrets))

	// Admin write — protected by API key
	mux.HandleFunc("POST /api/v1/admin/changes", h.requireKey(h.postChanges))
	mux.HandleFunc("DELETE /api/v1/admin/changes", h.requireKey(h.deleteChanges))
	mux.HandleFunc("POST /api/v1/admin/reload", h.requireKey(h.adminReload))
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
	resp := map[string]any{
		"status":          "ok",
		"version":         si.Version,
		"services_loaded": si.ServicesLoaded,
	}
	if !si.LastReloadAt.IsZero() {
		resp["last_reload_at"] = si.LastReloadAt.UTC().Format(time.RFC3339)
	}
	if si.IsDegraded {
		resp["status"] = "degraded"
		resp["is_degraded"] = true
		resp["last_reload_error"] = si.LastReloadError
	}
	respondJSON(w, http.StatusOK, resp)
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

	meta := configMeta(org, project, service, h.store.HeadVersion(), d.UpdatedAt)

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

	meta := configMeta(org, project, service, h.store.HeadVersion(), d.UpdatedAt)

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

// ---- admin write ----

// postChangesRequest matches the Phase-1 POST /api/v1/admin/changes payload.
// The `secrets` field from PRD v2.1 is intentionally not accepted here: the
// server rejects any unknown field (including secrets) with 400 so callers
// don't silently lose data while secret handling is unimplemented.
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

	result, err := h.store.ApplyChanges(r.Context(), req)
	if err != nil {
		respondError(w, err)
		return
	}

	status := "committed"
	code := http.StatusOK
	if result.ReloadFailed {
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
	respondJSON(w, code, resp)
}

// explainDecodeError gives a slightly friendlier hint for the common case of
// an unknown field (the PRD v2.1 `secrets` field, for example).
func (h *Handler) explainDecodeError(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "unknown field \"secrets\"") {
		return "secrets are not accepted by POST /api/v1/admin/changes in Phase-1; see docs for planned behaviour"
	}
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
		if h.apiKey == "" {
			next(w, r)
			return
		}
		if !authorized(r, h.apiKey) {
			respondErrorCode(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API key")
			return
		}
		next(w, r)
	}
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

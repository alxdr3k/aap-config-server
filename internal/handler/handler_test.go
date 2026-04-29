package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aap/config-server/internal/apperror"
	"github.com/aap/config-server/internal/handler"
	"github.com/aap/config-server/internal/parser"
	"github.com/aap/config-server/internal/registry"
	"github.com/aap/config-server/internal/secret"
	"github.com/aap/config-server/internal/store"
)

// --- fakes ---

type fakeStore struct {
	services               map[string]*store.ServiceData
	version                string
	failNextWrite          error
	nextReloadFailed       bool
	nextReloadErr          string
	nextApplyFailed        bool
	nextApplyErr           string
	nextDeleteReloadFailed bool
	nextDeleteReloadErr    string
	degraded               bool
	refreshErr             error
	reloadErr              error
	reloadUpdated          bool
	reloadCalls            int
	refreshCalls           int
	lastChange             *store.ChangeRequest
	sawSecretPlaintext     bool
}

type fakeVolumeReader struct {
	values          map[secret.Reference]string
	requests        []secret.Reference
	refreshRequests []secret.Reference
	err             error
}

func (f *fakeVolumeReader) Read(_ context.Context, ref secret.Reference) (secret.Value, error) {
	f.requests = append(f.requests, ref)
	return f.value(ref)
}

func (f *fakeVolumeReader) Refresh(_ context.Context, ref secret.Reference) (secret.Value, error) {
	f.refreshRequests = append(f.refreshRequests, ref)
	return f.value(ref)
}

func (f *fakeVolumeReader) value(ref secret.Reference) (secret.Value, error) {
	if f.err != nil {
		return secret.Value{}, f.err
	}
	value, ok := f.values[ref]
	if !ok {
		return secret.Value{}, errors.New("secret value not found")
	}
	return secret.NewValue([]byte(value)), nil
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		services: map[string]*store.ServiceData{},
		version:  "abc123",
	}
}

func (f *fakeStore) GetConfig(_ context.Context, org, project, service string) (*store.ServiceData, error) {
	key := org + "/" + project + "/" + service
	d, ok := f.services[key]
	if !ok {
		return nil, apperror.New(apperror.CodeNotFound, "service not found: "+key)
	}
	return d, nil
}

func (f *fakeStore) ListOrgs() []string {
	seen := map[string]bool{}
	for key := range f.services {
		parts := strings.SplitN(key, "/", 3)
		seen[parts[0]] = true
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func (f *fakeStore) ListProjects(org string) []string {
	seen := map[string]bool{}
	for key := range f.services {
		parts := strings.SplitN(key, "/", 3)
		if parts[0] == org {
			seen[parts[1]] = true
		}
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func (f *fakeStore) ListServices(org, project string) []store.ServiceInfo {
	var out []store.ServiceInfo
	prefix := org + "/" + project + "/"
	for key, d := range f.services {
		if strings.HasPrefix(key, prefix) {
			out = append(out, store.ServiceInfo{
				Name:       strings.TrimPrefix(key, prefix),
				HasConfig:  d.Config != nil,
				HasEnvVars: d.EnvVars != nil,
				HasSecrets: d.Secrets != nil,
			})
		}
	}
	return out
}

func (f *fakeStore) ApplyChanges(_ context.Context, req *store.ChangeRequest) (*store.ChangeResult, error) {
	f.lastChange = req
	f.sawSecretPlaintext = changeRequestContainsSecretPlaintext(req, "top-secret")
	if f.failNextWrite != nil {
		err := f.failNextWrite
		f.failNextWrite = nil
		return nil, err
	}
	if req.Org == "" || req.Project == "" || req.Service == "" {
		return nil, apperror.New(apperror.CodeValidation, "org/project/service required")
	}
	key := req.Org + "/" + req.Project + "/" + req.Service
	f.services[key] = &store.ServiceData{}
	f.version = "newcommit"
	return &store.ChangeResult{
		Version:      f.version,
		Files:        []string{"config.yaml"},
		ApplyFailed:  f.nextApplyFailed,
		ApplyError:   f.nextApplyErr,
		ReloadFailed: f.nextReloadFailed,
		ReloadError:  f.nextReloadErr,
	}, nil
}

func (f *fakeStore) DeleteChanges(_ context.Context, req *store.DeleteRequest) (*store.DeleteResult, error) {
	key := req.Org + "/" + req.Project + "/" + req.Service
	delete(f.services, key)
	f.version = "delcommit"
	return &store.DeleteResult{
		Version:      f.version,
		DeletedFiles: []string{"config.yaml"},
		ReloadFailed: f.nextDeleteReloadFailed,
		ReloadError:  f.nextDeleteReloadErr,
	}, nil
}

func (f *fakeStore) HeadVersion() string { return f.version }

func (f *fakeStore) RefreshFromRepo(_ context.Context) (bool, error) {
	f.refreshCalls++
	if f.refreshErr != nil {
		return false, f.refreshErr
	}
	return false, nil
}

func (f *fakeStore) ReloadFromRepo(_ context.Context) (bool, error) {
	f.reloadCalls++
	if f.reloadErr != nil {
		return false, f.reloadErr
	}
	// Force-reload clears the degraded flag on success.
	f.degraded = false
	return f.reloadUpdated, nil
}

func (f *fakeStore) IsDegraded() bool { return f.degraded }

func (f *fakeStore) StatusInfo() store.StoreStatus {
	return store.StoreStatus{
		Version:        f.version,
		ServicesLoaded: len(f.services),
		IsDegraded:     f.degraded,
	}
}

func changeRequestContainsSecretPlaintext(req *store.ChangeRequest, plaintext string) bool {
	for _, write := range req.Secrets {
		for _, value := range write.Data {
			if string(value.Bytes()) == plaintext {
				return true
			}
		}
	}
	return false
}

type alwaysReady struct{}

func (alwaysReady) IsReady() bool { return true }

type neverReady struct{}

func (neverReady) IsReady() bool { return false }

// --- test helpers ---

func newServer(t *testing.T, st handler.ConfigStore) *httptest.Server {
	return newServerWithAPIKey(t, st, "")
}

func newServerWithAPIKey(t *testing.T, st handler.ConfigStore, apiKey string, opts ...handler.Option) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	h := handler.New(st, alwaysReady{}, apiKey, opts...)
	h.Routes(mux)
	return httptest.NewServer(mux)
}

func get(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func getWithBearer(t *testing.T, srv *httptest.Server, path, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new GET %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func postJSON(t *testing.T, srv *httptest.Server, path string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func postJSONWithBearer(t *testing.T, srv *httptest.Server, path string, body any, token string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func deleteJSON(t *testing.T, srv *httptest.Server, path string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// --- tests ---

func TestHealthz(t *testing.T) {
	srv := newServer(t, newFakeStore())
	defer srv.Close()

	resp := get(t, srv, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: want 200, got %d", resp.StatusCode)
	}
}

func TestReadyz_Ready(t *testing.T) {
	mux := http.NewServeMux()
	h := handler.New(newFakeStore(), alwaysReady{}, "")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp := get(t, srv, "/readyz")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("readyz (ready): want 200, got %d", resp.StatusCode)
	}
}

func TestReadyz_NotReady(t *testing.T) {
	mux := http.NewServeMux()
	h := handler.New(newFakeStore(), neverReady{}, "")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp := get(t, srv, "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz (not ready): want 503, got %d", resp.StatusCode)
	}
}

func TestGetConfig_NotFound(t *testing.T) {
	srv := newServer(t, newFakeStore())
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/missing/config")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestGetConfig_Found(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		Config: &parser.ServiceConfig{
			Version:  "1",
			Metadata: parser.ServiceMetadata{Service: "svc", Org: "org", Project: "proj"},
			Config: map[string]any{
				"router_settings": map[string]any{"num_retries": 3},
			},
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)

	meta, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatal("missing metadata")
	}
	if meta["service"] != "svc" {
		t.Errorf("service: want svc, got %v", meta["service"])
	}
	if meta["version"] != "abc123" {
		t.Errorf("version: want abc123, got %v", meta["version"])
	}
}

func TestGetEnvVars_Found(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVars: &parser.EnvVarsConfig{
			EnvVars: parser.EnvVars{
				Plain:      map[string]string{"LOG_LEVEL": "INFO"},
				SecretRefs: map[string]string{"API_KEY": "my-secret"},
			},
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	envVars := body["env_vars"].(map[string]any)
	plain := envVars["plain"].(map[string]any)
	if plain["LOG_LEVEL"] != "INFO" {
		t.Errorf("LOG_LEVEL: want INFO, got %v", plain["LOG_LEVEL"])
	}
}

func TestGetEnvVars_ResolveSecrets(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVars: &parser.EnvVarsConfig{
			EnvVars: parser.EnvVars{
				Plain:      map[string]string{"LOG_LEVEL": "INFO"},
				SecretRefs: map[string]string{"API_KEY": "litellm-api-key"},
			},
		},
		Secrets: &parser.SecretsConfig{
			Secrets: []parser.SecretEntry{
				{
					ID: "litellm-api-key",
					K8sSecret: parser.K8sSecret{
						Namespace: "ai-platform",
						Name:      "litellm-secrets",
						Key:       "api-key",
					},
				},
			},
		},
	}
	ref := secret.Reference{
		ID:        "litellm-api-key",
		Namespace: "ai-platform",
		Name:      "litellm-secrets",
		Key:       "api-key",
	}
	reader := &fakeVolumeReader{values: map[secret.Reference]string{ref: "top-secret"}}
	var auditLogs bytes.Buffer
	srv := newServerWithAPIKey(t, st, "secret-key", handler.WithSecretDependencies(secret.Dependencies{
		VolumeReader: reader,
		Auditor: secret.NewSlogAuditorWithLogger(true,
			slog.New(slog.NewJSONHandler(&auditLogs, nil))),
	}))
	defer srv.Close()

	resp := getWithBearer(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?resolve_secrets=true", "secret-key")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control: got %q", got)
	}
	if got := resp.Header.Get("ETag"); got != "" {
		t.Fatalf("ETag should be omitted for resolved secrets, got %q", got)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	envVars := body["env_vars"].(map[string]any)
	plain := envVars["plain"].(map[string]any)
	if plain["LOG_LEVEL"] != "INFO" {
		t.Errorf("LOG_LEVEL: want INFO, got %v", plain["LOG_LEVEL"])
	}
	secrets := envVars["secrets"].(map[string]any)
	if secrets["API_KEY"] != "top-secret" {
		t.Fatalf("resolved API_KEY: got %v", secrets["API_KEY"])
	}
	if _, ok := envVars["secret_refs"]; ok {
		t.Fatal("resolve_secrets=true response must not include secret_refs")
	}
	if len(reader.refreshRequests) != 1 || reader.refreshRequests[0] != ref {
		t.Fatalf("reader refresh requests: got %+v", reader.refreshRequests)
	}
	if len(reader.requests) != 0 {
		t.Fatalf("resolve should force refresh instead of cached read, got reads %+v", reader.requests)
	}
	logText := auditLogs.String()
	for _, want := range []string{"secret_env_resolve", "success", "org", "proj", "svc", "litellm-api-key"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("audit log missing %q: %s", want, logText)
		}
	}
	if strings.Contains(logText, "top-secret") {
		t.Fatalf("audit log leaked plaintext: %s", logText)
	}
}

func TestGetEnvVars_ResolveSecretsRequiresAuth(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVars: &parser.EnvVarsConfig{EnvVars: parser.EnvVars{
			SecretRefs: map[string]string{"API_KEY": "litellm-api-key"},
		}},
	}
	srv := newServerWithAPIKey(t, st, "secret-key", handler.WithSecretDependencies(secret.Dependencies{
		VolumeReader: &fakeVolumeReader{values: map[secret.Reference]string{}},
	}))
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?resolve_secrets=true")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestGetEnvVars_ResolveSecretsRejectsDuplicateSecretIDs(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVars: &parser.EnvVarsConfig{EnvVars: parser.EnvVars{
			SecretRefs: map[string]string{"API_KEY": "dup-id"},
		}},
		Secrets: &parser.SecretsConfig{
			Secrets: []parser.SecretEntry{
				{
					ID:        "dup-id",
					K8sSecret: parser.K8sSecret{Namespace: "ns-a", Name: "secret-a", Key: "api-key"},
				},
				{
					ID:        "dup-id",
					K8sSecret: parser.K8sSecret{Namespace: "ns-b", Name: "secret-b", Key: "api-key"},
				},
			},
		},
	}
	reader := &fakeVolumeReader{values: map[secret.Reference]string{}}
	srv := newServerWithAPIKey(t, st, "secret-key", handler.WithSecretDependencies(secret.Dependencies{
		VolumeReader: reader,
	}))
	defer srv.Close()

	resp := getWithBearer(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?resolve_secrets=true", "secret-key")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", resp.StatusCode)
	}
	if len(reader.refreshRequests) != 0 {
		t.Fatalf("duplicate secret IDs should fail before reading mounted values, got %+v", reader.refreshRequests)
	}
}

func TestGetEnvVars_ResolveSecretsRejectsInvalidQuery(t *testing.T) {
	srv := newServer(t, newFakeStore())
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?resolve_secrets=maybe")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestListOrgs(t *testing.T) {
	st := newFakeStore()
	st.services["org1/proj/svc"] = &store.ServiceData{}
	st.services["org2/proj/svc"] = &store.ServiceData{}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	orgs := body["orgs"].([]any)
	if len(orgs) != 2 {
		t.Errorf("expected 2 orgs, got %d", len(orgs))
	}
}

func TestPostChanges(t *testing.T) {
	srv := newServer(t, newFakeStore())
	defer srv.Close()

	body := map[string]any{
		"org":     "myorg",
		"project": "proj",
		"service": "svc",
		"config": map[string]any{
			"router_settings": map[string]any{"num_retries": 3},
		},
		"message": "test change",
	}
	resp := postJSON(t, srv, "/api/v1/admin/changes", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["status"] != "committed" {
		t.Errorf("status: want committed, got %v", result["status"])
	}
	if result["version"] == "" {
		t.Error("expected non-empty version")
	}
}

func TestPostChanges_InvalidBody(t *testing.T) {
	srv := newServer(t, newFakeStore())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/admin/changes",
		strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestDeleteChanges(t *testing.T) {
	st := newFakeStore()
	st.services["myorg/proj/svc"] = &store.ServiceData{}
	srv := newServer(t, st)
	defer srv.Close()

	body := map[string]any{
		"org":     "myorg",
		"project": "proj",
		"service": "svc",
	}
	resp := deleteJSON(t, srv, "/api/v1/admin/changes", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["status"] != "deleted" {
		t.Errorf("status: want deleted, got %v", result["status"])
	}
}

func TestRespondError_GitPush(t *testing.T) {
	st := newFakeStore()
	// Make ApplyChanges return a GIT_PUSH_FAILED error.
	st.failNextWrite = apperror.New(apperror.CodeGitPush, "push failed")

	srv := newServer(t, st)
	defer srv.Close()

	body := map[string]any{
		"org": "o", "project": "p", "service": "s",
		"config": map[string]any{},
	}
	resp := postJSON(t, srv, "/api/v1/admin/changes", body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("git push failure: want 503, got %d", resp.StatusCode)
	}
}

func TestAPIKeyAuth(t *testing.T) {
	mux := http.NewServeMux()
	h := handler.New(newFakeStore(), alwaysReady{}, "secret-key")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := map[string]any{
		"org": "o", "project": "p", "service": "s",
		"config": map[string]any{},
	}
	b, _ := json.Marshal(body)

	// No key → 401
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/admin/changes", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no key: want 401, got %d", resp.StatusCode)
	}

	// Wrong key → 401
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/admin/changes", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "wrong")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong key: want 401, got %d", resp.StatusCode)
	}

	// Correct key → 200
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/admin/changes", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "secret-key")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("correct key: want 200, got %d", resp.StatusCode)
	}
}

func TestAPIKeyAuth_BearerHeader(t *testing.T) {
	mux := http.NewServeMux()
	h := handler.New(newFakeStore(), alwaysReady{}, "secret-key")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{
		"org": "o", "project": "p", "service": "s",
		"config": map[string]any{},
	})

	cases := []struct {
		name     string
		header   string
		value    string
		wantCode int
	}{
		{"bearer correct", "Authorization", "Bearer secret-key", http.StatusOK},
		{"bearer wrong", "Authorization", "Bearer nope", http.StatusUnauthorized},
		{"bearer wrong scheme", "Authorization", "Basic secret-key", http.StatusUnauthorized},
		{"x-api-key alias", "X-API-Key", "secret-key", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/admin/changes", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(tc.header, tc.value)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			if resp.StatusCode != tc.wantCode {
				t.Errorf("%s: want %d, got %d", tc.name, tc.wantCode, resp.StatusCode)
			}
		})
	}
}

func TestAppRegistryWebhook_UpsertAndDelete(t *testing.T) {
	cache := registry.NewCache()
	srv := newServerWithAPIKey(t, newFakeStore(), "secret-key", handler.WithAppRegistry(cache))
	defer srv.Close()

	upsertBody := map[string]any{
		"action": "upsert",
		"app": map[string]any{
			"org":        "myorg",
			"project":    "ai",
			"name":       "litellm",
			"updated_at": "2026-04-29T10:00:00Z",
		},
	}
	resp := postJSONWithBearer(t, srv, "/api/v1/admin/app-registry/webhook", upsertBody, "secret-key")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upsert: want 200, got %d", resp.StatusCode)
	}
	apps := cache.List()
	if len(apps) != 1 || apps[0].Org != "myorg" || apps[0].Project != "ai" || apps[0].Service != "litellm" {
		t.Fatalf("cached apps after upsert: %+v", apps)
	}

	deleteBody := map[string]any{
		"action":     "delete",
		"org":        "myorg",
		"project":    "ai",
		"service":    "litellm",
		"updated_at": "2026-04-29T10:01:00Z",
	}
	resp = postJSONWithBearer(t, srv, "/api/v1/admin/app-registry/webhook", deleteBody, "secret-key")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: want 200, got %d", resp.StatusCode)
	}
	if apps := cache.List(); len(apps) != 0 {
		t.Fatalf("cached apps after delete: %+v", apps)
	}
}

func TestAppRegistryWebhook_RequiresAuth(t *testing.T) {
	cache := registry.NewCache()
	srv := newServerWithAPIKey(t, newFakeStore(), "secret-key", handler.WithAppRegistry(cache))
	defer srv.Close()

	resp := postJSON(t, srv, "/api/v1/admin/app-registry/webhook", map[string]any{
		"action": "upsert",
		"app": map[string]any{
			"org":        "myorg",
			"project":    "ai",
			"name":       "litellm",
			"updated_at": "2026-04-29T10:00:00Z",
		},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	if apps := cache.List(); len(apps) != 0 {
		t.Fatalf("unauthorized request should not update cache: %+v", apps)
	}
}

func TestAppRegistryWebhook_RejectsInvalidAction(t *testing.T) {
	cache := registry.NewCache()
	srv := newServerWithAPIKey(t, newFakeStore(), "secret-key", handler.WithAppRegistry(cache))
	defer srv.Close()

	resp := postJSONWithBearer(t, srv, "/api/v1/admin/app-registry/webhook", map[string]any{
		"action": "bogus",
		"app": map[string]any{
			"org":        "myorg",
			"project":    "ai",
			"name":       "litellm",
			"updated_at": "2026-04-29T10:00:00Z",
		},
	}, "secret-key")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestAppRegistryWebhook_RequiresUpdatedAt(t *testing.T) {
	cache := registry.NewCache()
	srv := newServerWithAPIKey(t, newFakeStore(), "secret-key", handler.WithAppRegistry(cache))
	defer srv.Close()

	resp := postJSONWithBearer(t, srv, "/api/v1/admin/app-registry/webhook", map[string]any{
		"action": "upsert",
		"app": map[string]any{
			"org":     "myorg",
			"project": "ai",
			"name":    "litellm",
		},
	}, "secret-key")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	if apps := cache.List(); len(apps) != 0 {
		t.Fatalf("missing updated_at should not update cache: %+v", apps)
	}
}

func TestPostChanges_AcceptsSecretsField(t *testing.T) {
	st := newFakeStore()
	srv := newServer(t, st)
	defer srv.Close()

	body := map[string]any{
		"org": "o", "project": "p", "service": "s",
		"config": map[string]any{},
		"secrets": map[string]any{
			"litellm-secrets": map[string]any{
				"namespace": "ai-platform",
				"data": map[string]any{
					"master-key": "top-secret",
				},
			},
		},
	}
	resp := postJSON(t, srv, "/api/v1/admin/changes", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 for accepted `secrets` field, got %d", resp.StatusCode)
	}
	if st.lastChange == nil {
		t.Fatal("store did not receive change request")
	}
	got := st.lastChange.Secrets["litellm-secrets"]
	if got.Namespace != "ai-platform" {
		t.Fatalf("secret namespace: got %q", got.Namespace)
	}
	if !st.sawSecretPlaintext {
		t.Fatal("secret plaintext was not passed to store boundary")
	}
	if string(got.Data["master-key"].Bytes()) != "" {
		t.Fatal("handler should destroy secret plaintext after store boundary returns")
	}
}

func TestPostChanges_RejectsUnknownField(t *testing.T) {
	srv := newServer(t, newFakeStore())
	defer srv.Close()

	body := map[string]any{
		"org": "o", "project": "p", "service": "s",
		"config": map[string]any{},
		"bogus":  "value",
	}
	resp := postJSON(t, srv, "/api/v1/admin/changes", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for unknown field, got %d", resp.StatusCode)
	}
}

func TestPostChanges_ReloadFailedReported(t *testing.T) {
	st := newFakeStore()
	st.nextReloadFailed = true
	st.nextReloadErr = "snapshot refused: bad yaml at foo"

	srv := newServer(t, st)
	defer srv.Close()

	body := map[string]any{
		"org": "o", "project": "p", "service": "s",
		"config": map[string]any{"k": "v"},
	}
	resp := postJSON(t, srv, "/api/v1/admin/changes", body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when reload failed after commit, got %d", resp.StatusCode)
	}

	var body2 map[string]any
	decodeJSON(t, resp, &body2)
	if body2["status"] != "committed_but_reload_failed" {
		t.Errorf("status: want committed_but_reload_failed, got %v", body2["status"])
	}
	if body2["reload_error"] == nil || body2["reload_error"] == "" {
		t.Errorf("reload_error missing: %v", body2)
	}
}

func TestPostChanges_ApplyFailedReported(t *testing.T) {
	st := newFakeStore()
	st.nextApplyFailed = true
	st.nextApplyErr = "apply sealed secret ai-platform/litellm-secrets: boom"
	srv := newServer(t, st)
	defer srv.Close()

	body := map[string]any{
		"org": "o", "project": "p", "service": "s",
		"secrets": map[string]any{
			"litellm-secrets": map[string]any{
				"namespace": "ai-platform",
				"data":      map[string]any{"master-key": "top-secret"},
			},
		},
	}
	resp := postJSON(t, srv, "/api/v1/admin/changes", body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}

	var env map[string]any
	decodeJSON(t, resp, &env)
	if env["status"] != "committed_but_apply_failed" {
		t.Fatalf("status: got %v", env["status"])
	}
	if !strings.Contains(env["apply_error"].(string), "apply sealed secret") {
		t.Fatalf("missing apply_error context: %v", env["apply_error"])
	}
}

func TestGetSecrets_RequiresAuth(t *testing.T) {
	mux := http.NewServeMux()
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	h := handler.New(st, alwaysReady{}, "secret-key")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// No key → 401.
	resp, err := http.Get(srv.URL + "/api/v1/orgs/org/projects/proj/services/svc/secrets")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401 without key, got %d", resp.StatusCode)
	}

	// With bearer → 200.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/orgs/org/projects/proj/services/svc/secrets", nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET authed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 with key, got %d", resp.StatusCode)
	}
}

func TestErrorResponse_IsJSONEnvelope(t *testing.T) {
	srv := newServer(t, newFakeStore())
	defer srv.Close()

	// Force a 404 via unknown service.
	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/nope/config")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
	var env map[string]any
	decodeJSON(t, resp, &env)
	errObj, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error envelope, got %v", env)
	}
	if errObj["code"] != "not_found" {
		t.Errorf("code: want not_found, got %v", errObj["code"])
	}
}

func TestGetConfig_HasUpdatedAt(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		Config: &parser.ServiceConfig{Version: "1"},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	meta := body["metadata"].(map[string]any)
	if _, ok := meta["updated_at"]; !ok {
		t.Error("metadata.updated_at missing from response")
	}
}

func TestDeleteChanges_ReloadFailed(t *testing.T) {
	st := newFakeStore()
	st.services["myorg/proj/svc"] = &store.ServiceData{}
	st.nextDeleteReloadFailed = true
	st.nextDeleteReloadErr = "refusing to swap snapshot: bad yaml"

	srv := newServer(t, st)
	defer srv.Close()

	body := map[string]any{"org": "myorg", "project": "proj", "service": "svc"}
	resp := deleteJSON(t, srv, "/api/v1/admin/changes", body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when reload failed after delete, got %d", resp.StatusCode)
	}

	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["status"] != "deleted_but_reload_failed" {
		t.Errorf("status: want deleted_but_reload_failed, got %v", result["status"])
	}
	if result["reload_error"] == nil || result["reload_error"] == "" {
		t.Errorf("reload_error missing: %v", result)
	}
}

func TestReadyz_Degraded(t *testing.T) {
	st := newFakeStore()
	st.degraded = true
	mux := http.NewServeMux()
	h := handler.New(st, alwaysReady{}, "")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp := get(t, srv, "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz degraded: want 503, got %d", resp.StatusCode)
	}
}

func TestStatus_Enriched(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["status"] != "ok" {
		t.Errorf("status: want ok, got %v", body["status"])
	}
	if body["version"] != "abc123" {
		t.Errorf("version: want abc123, got %v", body["version"])
	}
	if body["services_loaded"] == nil {
		t.Error("services_loaded missing from /api/v1/status response")
	}
}

func TestStatus_Degraded(t *testing.T) {
	st := newFakeStore()
	st.degraded = true
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 (not 503) for /api/v1/status even when degraded, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["status"] != "degraded" {
		t.Errorf("status: want degraded, got %v", body["status"])
	}
	if body["is_degraded"] != true {
		t.Errorf("is_degraded: want true, got %v", body["is_degraded"])
	}
}

func TestAdminReload_Error(t *testing.T) {
	st := newFakeStore()
	st.reloadErr = errors.New("refusing to swap snapshot: 1 file(s) failed to parse")
	mux := http.NewServeMux()
	h := handler.New(st, alwaysReady{}, "")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/admin/reload", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST reload: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 on reload failure, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["status"] != "reload_failed" {
		t.Errorf("status: want reload_failed, got %v", body["status"])
	}
	if body["reload_error"] == nil || body["reload_error"] == "" {
		t.Errorf("reload_error missing: %v", body)
	}
}

// TestAdminReload_DegradedNoHeadUpdate_Returns503 exercises the P1 fix from
// the 2026-04 review. Background poll's RefreshFromRepo skips reload when
// HEAD has not moved, but an operator-triggered POST /api/v1/admin/reload MUST re-
// parse the current checkout — otherwise a degraded store that has lost its
// reason to repull would keep returning a bogus 200 OK. We simulate that by
// seeding a store whose ReloadFromRepo reports a parse failure and asserting
// the handler surfaces a 503 reload_failed.
func TestAdminReload_DegradedNoHeadUpdate_Returns503(t *testing.T) {
	st := newFakeStore()
	st.degraded = true
	// RefreshFromRepo would succeed (no HEAD update → no reload → 200), but
	// ReloadFromRepo force-reloads and re-hits the underlying parse failure.
	st.refreshErr = nil
	st.reloadErr = errors.New("refusing to swap snapshot: 1 file(s) failed to parse: bad.yaml")

	mux := http.NewServeMux()
	h := handler.New(st, alwaysReady{}, "")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/admin/reload", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST reload: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("degraded + no HEAD move: want 503, got %d", resp.StatusCode)
	}
	if st.reloadCalls != 1 {
		t.Errorf("expected ReloadFromRepo to be called exactly once, got %d", st.reloadCalls)
	}
	if st.refreshCalls != 0 {
		t.Errorf("admin reload must not route through RefreshFromRepo, got %d calls", st.refreshCalls)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["status"] != "reload_failed" {
		t.Errorf("status: want reload_failed, got %v", body["status"])
	}
}

// TestAdminReload_ForceReloadsWhenHeadUnchanged asserts the happy path of the
// same P1 fix: if the operator hits POST /api/v1/admin/reload while HEAD has not
// moved, the handler still force-reloads and returns 200. `updated` may be
// true (recovered from degraded) or false (no-op); the shape must be 200/ok.
func TestAdminReload_ForceReloadsWhenHeadUnchanged(t *testing.T) {
	st := newFakeStore()
	st.reloadUpdated = false // HEAD didn't move, reload was a no-op

	mux := http.NewServeMux()
	h := handler.New(st, alwaysReady{}, "")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/admin/reload", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST reload: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 on successful force reload, got %d", resp.StatusCode)
	}
	if st.reloadCalls != 1 {
		t.Errorf("expected ReloadFromRepo to be called exactly once, got %d", st.reloadCalls)
	}
}

func TestAPIKeyAuth_BearerCaseInsensitive(t *testing.T) {
	mux := http.NewServeMux()
	h := handler.New(newFakeStore(), alwaysReady{}, "secret-key")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{
		"org": "o", "project": "p", "service": "s",
		"config": map[string]any{},
	})

	cases := []struct {
		name     string
		authVal  string
		wantCode int
	}{
		{"canonical Bearer", "Bearer secret-key", http.StatusOK},
		{"lowercase bearer", "bearer secret-key", http.StatusOK},
		{"uppercase BEARER", "BEARER secret-key", http.StatusOK},
		{"wrong token", "bearer wrong-key", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/admin/changes", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", tc.authVal)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			if resp.StatusCode != tc.wantCode {
				t.Errorf("%s: want %d, got %d", tc.name, tc.wantCode, resp.StatusCode)
			}
		})
	}
}

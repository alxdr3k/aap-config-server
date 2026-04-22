package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aap/config-server/internal/apperror"
	"github.com/aap/config-server/internal/handler"
	"github.com/aap/config-server/internal/parser"
	"github.com/aap/config-server/internal/store"
)

// --- fakes ---

type fakeStore struct {
	services      map[string]*store.ServiceData
	version       string
	failNextWrite error
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
	return &store.ChangeResult{Version: f.version, Files: []string{"config.yaml"}}, nil
}

func (f *fakeStore) DeleteChanges(_ context.Context, req *store.DeleteRequest) (*store.DeleteResult, error) {
	key := req.Org + "/" + req.Project + "/" + req.Service
	delete(f.services, key)
	f.version = "delcommit"
	return &store.DeleteResult{Version: f.version, DeletedFiles: []string{"config.yaml"}}, nil
}

func (f *fakeStore) HeadVersion() string { return f.version }

func (f *fakeStore) RefreshFromRepo(_ context.Context) (bool, error) { return false, nil }

type alwaysReady struct{}

func (alwaysReady) IsReady() bool { return true }

type neverReady struct{}

func (neverReady) IsReady() bool { return false }

// --- test helpers ---

func newServer(t *testing.T, st handler.ConfigStore) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	h := handler.New(st, alwaysReady{}, "")
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


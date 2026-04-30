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
	"time"

	"github.com/aap/config-server/internal/apperror"
	"github.com/aap/config-server/internal/handler"
	"github.com/aap/config-server/internal/parser"
	"github.com/aap/config-server/internal/registry"
	"github.com/aap/config-server/internal/secret"
	"github.com/aap/config-server/internal/store"
)

// --- fakes ---

type fakeStore struct {
	services                   map[string]*store.ServiceData
	version                    string
	failNextWrite              error
	nextReloadFailed           bool
	nextReloadErr              string
	nextApplyFailed            bool
	nextApplyErr               string
	nextDeleteReloadFailed     bool
	nextDeleteReloadErr        string
	degraded                   bool
	refreshErr                 error
	reloadErr                  error
	reloadUpdated              bool
	reloadCalls                int
	refreshCalls               int
	resourceVersions           map[string]string
	history                    []store.HistoryEntry
	historyOpts                store.HistoryOptions
	configAtVersion            map[string]*store.ServiceData
	configVersionArg           string
	inheritedConfigVersionArg  string
	inheritedConfigAtVersion   map[string]*store.ServiceData
	envVarsAtVersion           map[string]*store.ServiceData
	envVarsVersionArg          string
	inheritedEnvVarsVersionArg string
	inheritedEnvVarsAtVersion  map[string]*store.ServiceData
	waitVersionCalls           int
	waitVersionArg             string
	waitForVersionChange       func(context.Context, string) (string, bool, error)
	lastChange                 *store.ChangeRequest
	lastRevert                 *store.RevertRequest
	revertResult               *store.RevertResult
	revertErr                  error
	sawSecretPlaintext         bool
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

func (f *fakeStore) GetConfigAtVersion(_ context.Context, org, project, service, version string) (*store.ServiceData, error) {
	f.configVersionArg = version
	if _, err := f.GetConfig(context.Background(), org, project, service); err != nil {
		return nil, err
	}
	if d, ok := f.configAtVersion[version]; ok {
		return d, nil
	}
	return nil, apperror.New(apperror.CodeNotFound, "historical config not found")
}

func (f *fakeStore) GetInheritedConfigAtVersion(
	_ context.Context,
	org, project, service, version string,
) (*store.ServiceData, error) {
	f.inheritedConfigVersionArg = version
	if _, err := f.GetConfig(context.Background(), org, project, service); err != nil {
		return nil, err
	}
	if d, ok := f.inheritedConfigAtVersion[version]; ok {
		return d, nil
	}
	return nil, apperror.New(apperror.CodeNotFound, "historical inherited config not found")
}

func (f *fakeStore) GetEnvVarsAtVersion(_ context.Context, org, project, service, version string) (*store.ServiceData, error) {
	f.envVarsVersionArg = version
	if _, err := f.GetConfig(context.Background(), org, project, service); err != nil {
		return nil, err
	}
	if d, ok := f.envVarsAtVersion[version]; ok {
		return d, nil
	}
	return nil, apperror.New(apperror.CodeNotFound, "historical env_vars not found")
}

func (f *fakeStore) GetInheritedEnvVarsAtVersion(
	_ context.Context,
	org, project, service, version string,
) (*store.ServiceData, error) {
	f.inheritedEnvVarsVersionArg = version
	if _, err := f.GetConfig(context.Background(), org, project, service); err != nil {
		return nil, err
	}
	if d, ok := f.inheritedEnvVarsAtVersion[version]; ok {
		return d, nil
	}
	return nil, apperror.New(apperror.CodeNotFound, "historical inherited env_vars not found")
}

func (f *fakeStore) History(_ context.Context, opts store.HistoryOptions) ([]store.HistoryEntry, error) {
	f.historyOpts = opts
	if _, err := f.GetConfig(context.Background(), opts.Org, opts.Project, opts.Service); err != nil {
		return nil, err
	}
	return f.history, nil
}

func (f *fakeStore) ApplyRevert(_ context.Context, req *store.RevertRequest) (*store.RevertResult, error) {
	f.lastRevert = req
	if f.revertErr != nil {
		return nil, f.revertErr
	}
	if f.revertResult != nil {
		return f.revertResult, nil
	}
	return &store.RevertResult{
		Version:       "revertcommit",
		TargetVersion: req.TargetVersion,
		UpdatedAt:     time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
		RestoredFiles: []string{"config.yaml"},
	}, nil
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

func (f *fakeStore) ResourceVersion(ctx context.Context, org, project, service, resource string) (string, string, error) {
	d, err := f.GetConfig(ctx, org, project, service)
	if err != nil {
		return "", f.version, err
	}
	switch resource {
	case "config":
		if d.ConfigResourceVersion != "" {
			return d.ConfigResourceVersion, f.version, nil
		}
	case "env_vars":
		if d.EnvVarsResourceVersion != "" {
			return d.EnvVarsResourceVersion, f.version, nil
		}
	}
	if f.resourceVersions != nil {
		if version := f.resourceVersions[resource]; version != "" {
			return version, f.version, nil
		}
	}
	return f.version, f.version, nil
}

func (f *fakeStore) WaitForVersionChange(ctx context.Context, version string) (string, bool, error) {
	f.waitVersionCalls++
	f.waitVersionArg = version
	if f.waitForVersionChange != nil {
		return f.waitForVersionChange(ctx, version)
	}
	if f.version != version {
		return f.version, true, nil
	}
	<-ctx.Done()
	return f.version, false, ctx.Err()
}

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

func getWithHeader(t *testing.T, srv *httptest.Server, path, name, value string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new GET %s: %v", path, err)
	}
	req.Header.Set(name, value)
	resp, err := http.DefaultClient.Do(req)
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

func jsonArrayContains(values any, want string) bool {
	list, ok := values.([]any)
	if !ok {
		return false
	}
	for _, value := range list {
		if value == want {
			return true
		}
	}
	return false
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

func TestGetConfig_ETagAndIfNoneMatch(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		ConfigResourceVersion: "config-v1",
		UpdatedAt:             time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC),
		Config: &parser.ServiceConfig{
			Config: map[string]any{"router_settings": map[string]any{"num_retries": 3}},
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag")
	}

	resp = getWithHeader(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config", "If-None-Match", etag)
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("ETag"); got != etag {
		t.Fatalf("304 ETag: got %q, want %q", got, etag)
	}

	st.services["org/proj/svc"].UpdatedAt = time.Date(2026, 4, 30, 10, 1, 0, 0, time.UTC)
	resp = getWithHeader(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config", "If-None-Match", etag)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("updated metadata should return 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("ETag"); got == "" || got == etag {
		t.Fatalf("updated metadata ETag should change, got %q old %q", got, etag)
	}
}

func TestGetConfig_ETagVariesByInheritView(t *testing.T) {
	st := newFakeStore()
	st.version = "head-inherit"
	st.services["org/proj/svc"] = &store.ServiceData{
		ConfigResourceVersion: "raw-v1",
		Config:                &parser.ServiceConfig{Config: map[string]any{"raw": true}},
		InheritedSources:      []store.DefaultsSource{{Scope: store.DefaultsScopeGlobal, HasConfig: true}},
		InheritedConfig:       &parser.ServiceConfig{Config: map[string]any{"raw": true, "global": true}},
	}
	srv := newServer(t, st)
	defer srv.Close()

	inheritedResp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config")
	rawResp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config?inherit=false")
	inheritedETag := inheritedResp.Header.Get("ETag")
	rawETag := rawResp.Header.Get("ETag")
	if inheritedETag == "" || rawETag == "" {
		t.Fatalf("expected both ETags, inherited=%q raw=%q", inheritedETag, rawETag)
	}
	if inheritedETag == rawETag {
		t.Fatalf("inherit views should have distinct ETags, both %q", inheritedETag)
	}
}

func TestGetConfig_DefaultInheritUsesInheritedConfig(t *testing.T) {
	st := newFakeStore()
	st.version = "head-inherit"
	st.services["org/proj/svc"] = &store.ServiceData{
		ConfigResourceVersion: "raw-v1",
		Config: &parser.ServiceConfig{Config: map[string]any{
			"service_only": true,
		}},
		InheritedSources: []store.DefaultsSource{{Scope: store.DefaultsScopeGlobal, HasConfig: true}},
		InheritedConfig: &parser.ServiceConfig{Config: map[string]any{
			"service_only": true,
			"global_only":  true,
		}},
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
	if meta["version"] != "head-inherit" {
		t.Fatalf("inherited metadata.version should use head version, got %v", meta["version"])
	}
	config := body["config"].(map[string]any)
	if config["global_only"] != true || config["service_only"] != true {
		t.Fatalf("config should use inherited view, got %#v", config)
	}
}

func TestGetConfig_InheritFalseUsesRawConfig(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		ConfigResourceVersion: "raw-v1",
		Config: &parser.ServiceConfig{Config: map[string]any{
			"service_only": true,
		}},
		InheritedSources: []store.DefaultsSource{{Scope: store.DefaultsScopeGlobal, HasConfig: true}},
		InheritedConfig: &parser.ServiceConfig{Config: map[string]any{
			"service_only": true,
			"global_only":  true,
		}},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config?inherit=false")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	meta := body["metadata"].(map[string]any)
	if meta["version"] != "raw-v1" {
		t.Fatalf("raw metadata.version should use resource version, got %v", meta["version"])
	}
	config := body["config"].(map[string]any)
	if _, ok := config["global_only"]; ok {
		t.Fatalf("inherit=false should omit inherited keys, got %#v", config)
	}
	if config["service_only"] != true {
		t.Fatalf("inherit=false should keep service config, got %#v", config)
	}
}

func TestGetConfig_InvalidInherit(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config?inherit=maybe")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestGetConfig_VersionParam(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	st.configAtVersion = map[string]*store.ServiceData{
		"old123": {
			Config: &parser.ServiceConfig{
				Config: map[string]any{
					"router_settings": map[string]any{"num_retries": 1},
				},
			},
			UpdatedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config?version=old123&inherit=false")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected versioned config ETag")
	}
	conditionalResp := getWithHeader(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config?version=old123&inherit=false", "If-None-Match", etag)
	if conditionalResp.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", conditionalResp.StatusCode)
	}
	if st.configVersionArg != "old123" {
		t.Fatalf("version arg: got %q", st.configVersionArg)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	meta := body["metadata"].(map[string]any)
	if meta["version"] != "old123" || meta["updated_at"] != "2026-03-01T10:00:00Z" {
		t.Fatalf("metadata: got %#v", meta)
	}
	config := body["config"].(map[string]any)
	settings := config["router_settings"].(map[string]any)
	if settings["num_retries"] != float64(1) {
		t.Fatalf("historical num_retries: got %#v", settings["num_retries"])
	}
}

func TestGetConfig_VersionParamDefaultInherit(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	st.inheritedConfigAtVersion = map[string]*store.ServiceData{
		"old123": {
			InheritedConfig: &parser.ServiceConfig{
				Config: map[string]any{
					"global_only": true,
				},
			},
			UpdatedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config?version=old123")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if st.inheritedConfigVersionArg != "old123" {
		t.Fatalf("inherited version arg: got %q", st.inheritedConfigVersionArg)
	}
	if st.configVersionArg != "" {
		t.Fatalf("raw version path should not be called, got %q", st.configVersionArg)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	config := body["config"].(map[string]any)
	if config["global_only"] != true {
		t.Fatalf("historical inherited config: got %#v", config)
	}
}

func TestGetHistory_Found(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	st.history = []store.HistoryEntry{
		{
			Version:      "v2",
			Message:      "update config",
			Author:       "admin@example.com",
			Timestamp:    time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC),
			FilesChanged: []string{"config.yaml"},
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/history?file=config&limit=1&before=v3")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if st.historyOpts.File != "config" || st.historyOpts.Limit != 1 || st.historyOpts.Before != "v3" {
		t.Fatalf("history opts: got %+v", st.historyOpts)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	meta := body["metadata"].(map[string]any)
	if meta["org"] != "org" || meta["project"] != "proj" || meta["service"] != "svc" {
		t.Fatalf("metadata: got %#v", meta)
	}
	history := body["history"].([]any)
	if len(history) != 1 {
		t.Fatalf("history length: want 1, got %d", len(history))
	}
	entry := history[0].(map[string]any)
	if entry["version"] != "v2" || entry["timestamp"] != "2026-03-10T10:00:00Z" {
		t.Fatalf("history entry: got %#v", entry)
	}
	if !jsonArrayContains(entry["files_changed"], "config.yaml") {
		t.Fatalf("files_changed missing config.yaml: %#v", entry["files_changed"])
	}
}

func TestGetHistory_NotFound(t *testing.T) {
	srv := newServer(t, newFakeStore())
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/missing/history")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestGetHistory_RejectsInvalidQuery(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	srv := newServer(t, st)
	defer srv.Close()

	tests := []struct {
		name string
		path string
	}{
		{"invalid file", "/api/v1/orgs/org/projects/proj/services/svc/history?file=sealed"},
		{"non integer limit", "/api/v1/orgs/org/projects/proj/services/svc/history?limit=many"},
		{"zero limit", "/api/v1/orgs/org/projects/proj/services/svc/history?limit=0"},
		{"over max limit", "/api/v1/orgs/org/projects/proj/services/svc/history?limit=101"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := get(t, srv, tc.path)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("want 400, got %d", resp.StatusCode)
			}
		})
	}
}

func TestGetConfigWatch_VersionMismatchReturnsConfig(t *testing.T) {
	st := newFakeStore()
	st.version = "newcommit"
	st.services["org/proj/svc"] = &store.ServiceData{
		Config: &parser.ServiceConfig{
			Config: map[string]any{
				"router_settings": map[string]any{"num_retries": 4},
			},
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config/watch?version=oldcommit")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if st.waitVersionCalls != 0 {
		t.Fatalf("stale version should return without waiting, got %d calls", st.waitVersionCalls)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	meta := body["metadata"].(map[string]any)
	if meta["version"] != "newcommit" {
		t.Fatalf("metadata.version: got %v", meta["version"])
	}
	config := body["config"].(map[string]any)
	router := config["router_settings"].(map[string]any)
	if router["num_retries"] != float64(4) {
		t.Fatalf("num_retries: got %v", router["num_retries"])
	}
}

func TestGetConfigWatch_TimeoutReturnsNotModified(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		Config: &parser.ServiceConfig{Config: map[string]any{"x": "y"}},
	}
	st.waitForVersionChange = func(ctx context.Context, version string) (string, bool, error) {
		<-ctx.Done()
		return version, false, ctx.Err()
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config/watch?version=abc123&timeout=1ms")
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", resp.StatusCode)
	}
	if st.waitVersionCalls != 1 || st.waitVersionArg != "abc123" {
		t.Fatalf("WaitForVersionChange calls: count=%d arg=%q", st.waitVersionCalls, st.waitVersionArg)
	}
}

func TestGetConfigWatch_InheritedVersionUsesHeadVersion(t *testing.T) {
	st := newFakeStore()
	st.version = "head-inherit"
	st.services["org/proj/svc"] = &store.ServiceData{
		ConfigResourceVersion: "raw-v1",
		Config:                &parser.ServiceConfig{Config: map[string]any{"raw": true}},
		InheritedSources:      []store.DefaultsSource{{Scope: store.DefaultsScopeGlobal, HasConfig: true}},
		InheritedConfig:       &parser.ServiceConfig{Config: map[string]any{"raw": true, "global": true}},
	}
	st.waitForVersionChange = func(ctx context.Context, version string) (string, bool, error) {
		<-ctx.Done()
		return version, false, ctx.Err()
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config/watch?version=head-inherit&timeout=1ms")
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", resp.StatusCode)
	}
	if st.waitVersionCalls != 1 || st.waitVersionArg != "head-inherit" {
		t.Fatalf("WaitForVersionChange calls: count=%d arg=%q", st.waitVersionCalls, st.waitVersionArg)
	}
}

func TestGetConfigWatch_RequiresVersion(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config/watch")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	if st.waitVersionCalls != 0 {
		t.Fatalf("missing version should not wait, got %d calls", st.waitVersionCalls)
	}
}

func TestGetConfigWatch_RejectsInvalidTimeout(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config/watch?version=abc123&timeout=31s")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	if st.waitVersionCalls != 0 {
		t.Fatalf("invalid timeout should not wait, got %d calls", st.waitVersionCalls)
	}
}

func TestGetEnvVarsWatch_VersionMismatchReturnsEnvVars(t *testing.T) {
	st := newFakeStore()
	st.version = "newcommit"
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVars: &parser.EnvVarsConfig{
			EnvVars: parser.EnvVars{
				Plain:      map[string]string{"LOG_LEVEL": "INFO"},
				SecretRefs: map[string]string{"API_KEY": "litellm-api-key"},
			},
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars/watch?version=oldcommit")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if st.waitVersionCalls != 0 {
		t.Fatalf("stale version should return without waiting, got %d calls", st.waitVersionCalls)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	meta := body["metadata"].(map[string]any)
	if meta["version"] != "newcommit" {
		t.Fatalf("metadata.version: got %v", meta["version"])
	}
	envVars := body["env_vars"].(map[string]any)
	plain := envVars["plain"].(map[string]any)
	if plain["LOG_LEVEL"] != "INFO" {
		t.Fatalf("LOG_LEVEL: got %v", plain["LOG_LEVEL"])
	}
	secretRefs := envVars["secret_refs"].(map[string]any)
	if secretRefs["API_KEY"] != "litellm-api-key" {
		t.Fatalf("API_KEY secret_ref: got %v", secretRefs["API_KEY"])
	}
	if _, ok := envVars["secrets"]; ok {
		t.Fatal("env_vars/watch must not resolve secret values")
	}
}

func TestGetEnvVarsWatch_TimeoutReturnsNotModified(t *testing.T) {
	st := newFakeStore()
	st.resourceVersions = map[string]string{"env_vars": "env-v1"}
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVars: &parser.EnvVarsConfig{EnvVars: parser.EnvVars{
			Plain: map[string]string{"LOG_LEVEL": "INFO"},
		}},
	}
	st.waitForVersionChange = func(ctx context.Context, version string) (string, bool, error) {
		<-ctx.Done()
		return version, false, ctx.Err()
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars/watch?version=env-v1&timeout=1ms")
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", resp.StatusCode)
	}
	if st.waitVersionCalls != 1 || st.waitVersionArg != "abc123" {
		t.Fatalf("WaitForVersionChange calls: count=%d arg=%q", st.waitVersionCalls, st.waitVersionArg)
	}
}

func TestGetEnvVarsWatch_IgnoresConfigOnlyVersionChange(t *testing.T) {
	st := newFakeStore()
	st.version = "head1"
	st.resourceVersions = map[string]string{"env_vars": "env-v1"}
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVars: &parser.EnvVarsConfig{EnvVars: parser.EnvVars{
			Plain: map[string]string{"LOG_LEVEL": "INFO"},
		}},
	}
	st.waitForVersionChange = func(ctx context.Context, version string) (string, bool, error) {
		if st.waitVersionCalls == 1 {
			st.version = "head2"
			return st.version, true, nil
		}
		<-ctx.Done()
		return st.version, false, ctx.Err()
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars/watch?version=env-v1&timeout=1ms")
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304 for unchanged env_vars across config-only version bump, got %d", resp.StatusCode)
	}
	if st.waitVersionCalls != 2 || st.waitVersionArg != "head2" {
		t.Fatalf("WaitForVersionChange calls: count=%d last arg=%q", st.waitVersionCalls, st.waitVersionArg)
	}
}

func TestGetEnvVarsWatch_TimeoutWinsOverHeadOnlyChanges(t *testing.T) {
	st := newFakeStore()
	st.version = "head1"
	st.resourceVersions = map[string]string{"env_vars": "env-v1"}
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVars: &parser.EnvVarsConfig{EnvVars: parser.EnvVars{
			Plain: map[string]string{"LOG_LEVEL": "INFO"},
		}},
	}
	st.waitForVersionChange = func(ctx context.Context, _ string) (string, bool, error) {
		<-ctx.Done()
		st.version = "head-after-timeout"
		return st.version, true, nil
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars/watch?version=env-v1&timeout=1ms")
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304 after timeout despite head-only change, got %d", resp.StatusCode)
	}
	if st.waitVersionCalls != 1 {
		t.Fatalf("watch should stop once timeout has expired, got %d waits", st.waitVersionCalls)
	}
}

func TestGetEnvVarsWatch_RequiresVersion(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars/watch")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	if st.waitVersionCalls != 0 {
		t.Fatalf("missing version should not wait, got %d calls", st.waitVersionCalls)
	}
}

func TestGetEnvVars_Found(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVarsResourceVersion: "env-v1",
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
	meta := body["metadata"].(map[string]any)
	if meta["version"] != "env-v1" {
		t.Fatalf("unresolved env vars metadata.version should use resource version, got %v", meta["version"])
	}
	envVars := body["env_vars"].(map[string]any)
	plain := envVars["plain"].(map[string]any)
	if plain["LOG_LEVEL"] != "INFO" {
		t.Errorf("LOG_LEVEL: want INFO, got %v", plain["LOG_LEVEL"])
	}
}

func TestGetEnvVars_ETagAndIfNoneMatch(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVarsResourceVersion: "env-v1",
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
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag")
	}

	resp = getWithHeader(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars", "If-None-Match", etag)
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("ETag"); got != etag {
		t.Fatalf("304 ETag: got %q, want %q", got, etag)
	}
}

func TestGetEnvVars_DefaultInheritUsesInheritedEnvVars(t *testing.T) {
	st := newFakeStore()
	st.version = "head-inherit"
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVarsResourceVersion: "raw-env-v1",
		EnvVars: &parser.EnvVarsConfig{
			EnvVars: parser.EnvVars{
				Plain:      map[string]string{"RAW_ONLY": "1"},
				SecretRefs: map[string]string{"RAW_SECRET": "raw-secret"},
			},
		},
		InheritedSources: []store.DefaultsSource{{Scope: store.DefaultsScopeGlobal, HasEnvVars: true}},
		InheritedEnvVars: &parser.EnvVarsConfig{
			EnvVars: parser.EnvVars{
				Plain:      map[string]string{"RAW_ONLY": "1", "GLOBAL_ONLY": "1"},
				SecretRefs: map[string]string{"RAW_SECRET": "raw-secret", "GLOBAL_SECRET": "global-secret"},
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
	meta := body["metadata"].(map[string]any)
	if meta["version"] != "head-inherit" {
		t.Fatalf("inherited metadata.version should use head version, got %v", meta["version"])
	}
	envVars := body["env_vars"].(map[string]any)
	plain := envVars["plain"].(map[string]any)
	secretRefs := envVars["secret_refs"].(map[string]any)
	if plain["GLOBAL_ONLY"] != "1" || plain["RAW_ONLY"] != "1" {
		t.Fatalf("plain env vars should use inherited view, got %#v", plain)
	}
	if secretRefs["GLOBAL_SECRET"] != "global-secret" || secretRefs["RAW_SECRET"] != "raw-secret" {
		t.Fatalf("secret refs should use inherited view, got %#v", secretRefs)
	}
}

func TestGetEnvVars_InheritFalseUsesRawEnvVars(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVarsResourceVersion: "raw-env-v1",
		EnvVars: &parser.EnvVarsConfig{
			EnvVars: parser.EnvVars{
				Plain:      map[string]string{"RAW_ONLY": "1"},
				SecretRefs: map[string]string{"RAW_SECRET": "raw-secret"},
			},
		},
		InheritedSources: []store.DefaultsSource{{Scope: store.DefaultsScopeGlobal, HasEnvVars: true}},
		InheritedEnvVars: &parser.EnvVarsConfig{
			EnvVars: parser.EnvVars{
				Plain:      map[string]string{"RAW_ONLY": "1", "GLOBAL_ONLY": "1"},
				SecretRefs: map[string]string{"RAW_SECRET": "raw-secret", "GLOBAL_SECRET": "global-secret"},
			},
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?inherit=false")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	meta := body["metadata"].(map[string]any)
	if meta["version"] != "raw-env-v1" {
		t.Fatalf("raw metadata.version should use resource version, got %v", meta["version"])
	}
	envVars := body["env_vars"].(map[string]any)
	plain := envVars["plain"].(map[string]any)
	if _, ok := plain["GLOBAL_ONLY"]; ok {
		t.Fatalf("inherit=false should omit inherited plain env, got %#v", plain)
	}
	if plain["RAW_ONLY"] != "1" {
		t.Fatalf("inherit=false should keep raw env, got %#v", plain)
	}
}

func TestGetEnvVars_VersionParam(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	st.envVarsAtVersion = map[string]*store.ServiceData{
		"old-env": {
			EnvVars: &parser.EnvVarsConfig{
				EnvVars: parser.EnvVars{
					Plain:      map[string]string{"LOG_LEVEL": "DEBUG"},
					SecretRefs: map[string]string{"API_KEY": "old-api-key"},
				},
			},
			UpdatedAt: time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC),
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?version=old-env&inherit=false")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected versioned env_vars ETag")
	}
	conditionalResp := getWithHeader(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?version=old-env&inherit=false", "If-None-Match", etag)
	if conditionalResp.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", conditionalResp.StatusCode)
	}
	if st.envVarsVersionArg != "old-env" {
		t.Fatalf("version arg: got %q", st.envVarsVersionArg)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	meta := body["metadata"].(map[string]any)
	if meta["version"] != "old-env" || meta["updated_at"] != "2026-03-02T10:00:00Z" {
		t.Fatalf("metadata: got %#v", meta)
	}
	envVars := body["env_vars"].(map[string]any)
	plain := envVars["plain"].(map[string]any)
	secretRefs := envVars["secret_refs"].(map[string]any)
	if plain["LOG_LEVEL"] != "DEBUG" || secretRefs["API_KEY"] != "old-api-key" {
		t.Fatalf("historical env_vars: got %#v", envVars)
	}
}

func TestGetEnvVars_VersionParamDefaultInherit(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	st.inheritedEnvVarsAtVersion = map[string]*store.ServiceData{
		"old-env": {
			InheritedEnvVars: &parser.EnvVarsConfig{
				EnvVars: parser.EnvVars{
					Plain:      map[string]string{"GLOBAL_ONLY": "1"},
					SecretRefs: map[string]string{"GLOBAL_SECRET": "global-secret"},
				},
			},
			UpdatedAt: time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC),
		},
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?version=old-env")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if st.inheritedEnvVarsVersionArg != "old-env" {
		t.Fatalf("inherited version arg: got %q", st.inheritedEnvVarsVersionArg)
	}
	if st.envVarsVersionArg != "" {
		t.Fatalf("raw env version path should not be called, got %q", st.envVarsVersionArg)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	envVars := body["env_vars"].(map[string]any)
	plain := envVars["plain"].(map[string]any)
	secretRefs := envVars["secret_refs"].(map[string]any)
	if plain["GLOBAL_ONLY"] != "1" || secretRefs["GLOBAL_SECRET"] != "global-secret" {
		t.Fatalf("historical inherited env_vars: got %#v", envVars)
	}
}

func TestGetEnvVars_RejectsVersionWithResolveSecrets(t *testing.T) {
	srv := newServer(t, newFakeStore())
	defer srv.Close()

	resp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?version=old-env&resolve_secrets=true")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestGetVersionedReads_EmptyResource(t *testing.T) {
	st := newFakeStore()
	st.services["org/proj/svc"] = &store.ServiceData{}
	st.configAtVersion = map[string]*store.ServiceData{
		"empty": {ConfigResourceVersion: "empty"},
	}
	st.envVarsAtVersion = map[string]*store.ServiceData{
		"empty": {EnvVarsResourceVersion: "empty"},
	}
	srv := newServer(t, st)
	defer srv.Close()

	configResp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/config?version=empty&inherit=false")
	if configResp.StatusCode != http.StatusOK {
		t.Fatalf("config: want 200, got %d", configResp.StatusCode)
	}
	var configBody map[string]any
	decodeJSON(t, configResp, &configBody)
	if len(configBody["config"].(map[string]any)) != 0 {
		t.Fatalf("config should be empty: %#v", configBody["config"])
	}

	envResp := get(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?version=empty&inherit=false")
	if envResp.StatusCode != http.StatusOK {
		t.Fatalf("env_vars: want 200, got %d", envResp.StatusCode)
	}
	var envBody map[string]any
	decodeJSON(t, envResp, &envBody)
	envVars := envBody["env_vars"].(map[string]any)
	if len(envVars["plain"].(map[string]any)) != 0 || len(envVars["secret_refs"].(map[string]any)) != 0 {
		t.Fatalf("env_vars should be empty: %#v", envVars)
	}
}

func TestGetEnvVars_ResolveSecrets(t *testing.T) {
	st := newFakeStore()
	st.version = "secret-head"
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVarsResourceVersion: "env-v1",
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
	meta := body["metadata"].(map[string]any)
	if meta["version"] != "secret-head" {
		t.Fatalf("resolved env vars metadata.version should use head version, got %v", meta["version"])
	}
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

func TestGetEnvVars_ResolveSecretsUsesInheritedEnvVars(t *testing.T) {
	st := newFakeStore()
	st.version = "secret-head"
	st.services["org/proj/svc"] = &store.ServiceData{
		EnvVarsResourceVersion: "raw-env-v1",
		EnvVars: &parser.EnvVarsConfig{
			EnvVars: parser.EnvVars{
				Plain: map[string]string{"RAW_ONLY": "1"},
			},
		},
		InheritedSources: []store.DefaultsSource{{Scope: store.DefaultsScopeGlobal, HasEnvVars: true}},
		InheritedEnvVars: &parser.EnvVarsConfig{
			EnvVars: parser.EnvVars{
				Plain:      map[string]string{"RAW_ONLY": "1", "GLOBAL_ONLY": "1"},
				SecretRefs: map[string]string{"GLOBAL_SECRET": "global-secret-id"},
			},
		},
		Secrets: &parser.SecretsConfig{
			Secrets: []parser.SecretEntry{
				{
					ID: "global-secret-id",
					K8sSecret: parser.K8sSecret{
						Namespace: "ai-platform",
						Name:      "global-secrets",
						Key:       "token",
					},
				},
			},
		},
	}
	ref := secret.Reference{
		ID:        "global-secret-id",
		Namespace: "ai-platform",
		Name:      "global-secrets",
		Key:       "token",
	}
	reader := &fakeVolumeReader{values: map[secret.Reference]string{ref: "global-secret"}}
	srv := newServerWithAPIKey(t, st, "secret-key", handler.WithSecretDependencies(secret.Dependencies{
		VolumeReader: reader,
	}))
	defer srv.Close()

	resp := getWithBearer(t, srv, "/api/v1/orgs/org/projects/proj/services/svc/env_vars?resolve_secrets=true", "secret-key")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)
	envVars := body["env_vars"].(map[string]any)
	plain := envVars["plain"].(map[string]any)
	if plain["GLOBAL_ONLY"] != "1" || plain["RAW_ONLY"] != "1" {
		t.Fatalf("plain env vars should use inherited view, got %#v", plain)
	}
	secrets := envVars["secrets"].(map[string]any)
	if secrets["GLOBAL_SECRET"] != "global-secret" {
		t.Fatalf("resolved inherited secret: got %#v", secrets)
	}
	if len(reader.refreshRequests) != 1 || reader.refreshRequests[0] != ref {
		t.Fatalf("reader refresh requests: got %+v", reader.refreshRequests)
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

func TestPostChanges_PassesServiceLevelPayloadWhenInheritedReadsExist(t *testing.T) {
	st := newFakeStore()
	st.services["myorg/proj/svc"] = &store.ServiceData{
		Config: &parser.ServiceConfig{Config: map[string]any{
			"raw": true,
		}},
		InheritedSources: []store.DefaultsSource{{Scope: store.DefaultsScopeGlobal, HasConfig: true, HasEnvVars: true}},
		InheritedConfig: &parser.ServiceConfig{Config: map[string]any{
			"raw":     true,
			"default": true,
		}},
		InheritedEnvVars: &parser.EnvVarsConfig{EnvVars: parser.EnvVars{
			Plain: map[string]string{"GLOBAL_ENV": "1"},
		}},
	}
	srv := newServer(t, st)
	defer srv.Close()

	body := map[string]any{
		"org":     "myorg",
		"project": "proj",
		"service": "svc",
		"config": map[string]any{
			"raw": "updated",
		},
		"env_vars": map[string]any{
			"plain": map[string]any{"SERVICE_ENV": "updated"},
		},
		"message": "service-level update",
	}
	resp := postJSON(t, srv, "/api/v1/admin/changes", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if st.lastChange == nil {
		t.Fatal("expected ApplyChanges request")
	}
	if _, ok := st.lastChange.Config["default"]; ok {
		t.Fatalf("admin request should not merge inherited config into write payload: %#v", st.lastChange.Config)
	}
	if st.lastChange.Config["raw"] != "updated" {
		t.Fatalf("admin request should pass service config unchanged, got %#v", st.lastChange.Config)
	}
	if st.lastChange.EnvVars == nil {
		t.Fatal("expected env vars payload")
	}
	if _, ok := st.lastChange.EnvVars.Plain["GLOBAL_ENV"]; ok {
		t.Fatalf("admin request should not merge inherited env into write payload: %#v", st.lastChange.EnvVars.Plain)
	}
	if st.lastChange.EnvVars.Plain["SERVICE_ENV"] != "updated" {
		t.Fatalf("admin request should pass service env unchanged, got %#v", st.lastChange.EnvVars.Plain)
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

func TestPostRevert_RolledBack(t *testing.T) {
	st := newFakeStore()
	srv := newServer(t, st)
	defer srv.Close()

	body := map[string]any{
		"org":            "myorg",
		"project":        "proj",
		"service":        "litellm",
		"target_version": "target",
		"message":        "restore previous config",
	}
	resp := postJSON(t, srv, "/api/v1/admin/changes/revert", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var env map[string]any
	decodeJSON(t, resp, &env)
	if env["status"] != "rolled_back" {
		t.Fatalf("status: got %v", env["status"])
	}
	if env["version"] != "revertcommit" || env["target_version"] != "target" {
		t.Fatalf("versions: %v", env)
	}
	if st.lastRevert == nil {
		t.Fatal("store did not receive revert request")
	}
	if st.lastRevert.TargetVersion != "target" || st.lastRevert.Message != "restore previous config" {
		t.Fatalf("revert request: %+v", st.lastRevert)
	}
}

func TestPostRevert_Noop(t *testing.T) {
	st := newFakeStore()
	st.revertResult = &store.RevertResult{
		Version:       "abc123",
		TargetVersion: "abc123",
		UpdatedAt:     time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
		Noop:          true,
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := postJSON(t, srv, "/api/v1/admin/changes/revert", map[string]any{
		"org":            "myorg",
		"project":        "proj",
		"service":        "litellm",
		"target_version": "abc123",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var env map[string]any
	decodeJSON(t, resp, &env)
	if env["status"] != "noop" {
		t.Fatalf("status: got %v", env["status"])
	}
}

func TestPostRevert_ApplyAndReloadFailedReported(t *testing.T) {
	st := newFakeStore()
	st.revertResult = &store.RevertResult{
		Version:       "revertcommit",
		TargetVersion: "target",
		UpdatedAt:     time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
		ApplyFailed:   true,
		ApplyError:    "apply sealed secret ai-platform/remote-secrets: boom",
		ReloadFailed:  true,
		ReloadError:   "snapshot refused: bad yaml",
	}
	srv := newServer(t, st)
	defer srv.Close()

	resp := postJSON(t, srv, "/api/v1/admin/changes/revert", map[string]any{
		"org":            "myorg",
		"project":        "proj",
		"service":        "litellm",
		"target_version": "target",
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}

	var env map[string]any
	decodeJSON(t, resp, &env)
	if env["status"] != "rolled_back_but_apply_and_reload_failed" {
		t.Fatalf("status: got %v", env["status"])
	}
	if !strings.Contains(env["apply_error"].(string), "apply sealed secret") {
		t.Fatalf("missing apply_error context: %v", env["apply_error"])
	}
	if !strings.Contains(env["reload_error"].(string), "bad yaml") {
		t.Fatalf("missing reload_error context: %v", env["reload_error"])
	}
}

func TestPostRevert_RejectsUnknownField(t *testing.T) {
	srv := newServer(t, newFakeStore())
	defer srv.Close()

	resp := postJSON(t, srv, "/api/v1/admin/changes/revert", map[string]any{
		"org":            "myorg",
		"project":        "proj",
		"service":        "litellm",
		"target_version": "target",
		"bogus":          "value",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for unknown field, got %d", resp.StatusCode)
	}
}

func TestPostRevert_RequiresAuth(t *testing.T) {
	st := newFakeStore()
	srv := newServerWithAPIKey(t, st, "secret-key")
	defer srv.Close()

	resp := postJSON(t, srv, "/api/v1/admin/changes/revert", map[string]any{
		"org":            "myorg",
		"project":        "proj",
		"service":        "litellm",
		"target_version": "target",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	if st.lastRevert != nil {
		t.Fatalf("unauthorized request reached store: %+v", st.lastRevert)
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
	appRegistry, ok := body["app_registry"].(map[string]any)
	if !ok {
		t.Fatalf("app_registry missing from /api/v1/status response: %#v", body["app_registry"])
	}
	if appRegistry["status"] != "not_configured" {
		t.Errorf("app_registry.status: want not_configured, got %v", appRegistry["status"])
	}
}

func TestStatus_IncludesAppRegistryState(t *testing.T) {
	st := newFakeStore()
	cache := registry.NewCache()
	loadedAt := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	cache.Replace([]registry.App{{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		UpdatedAt: "2026-04-29T09:00:00Z",
	}}, loadedAt)
	srv := newServerWithAPIKey(t, st, "", handler.WithAppRegistry(cache))
	defer srv.Close()

	resp := get(t, srv, "/api/v1/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["status"] != "ok" {
		t.Fatalf("status: want ok, got %v", body["status"])
	}
	appRegistry, ok := body["app_registry"].(map[string]any)
	if !ok {
		t.Fatalf("app_registry missing from /api/v1/status response: %#v", body["app_registry"])
	}
	if appRegistry["status"] != "ok" {
		t.Errorf("app_registry.status: want ok, got %v", appRegistry["status"])
	}
	if appRegistry["apps_loaded"] != float64(1) {
		t.Errorf("app_registry.apps_loaded: want 1, got %v", appRegistry["apps_loaded"])
	}
	if appRegistry["last_loaded_at"] != loadedAt.Format(time.RFC3339) {
		t.Errorf("app_registry.last_loaded_at: want %s, got %v",
			loadedAt.Format(time.RFC3339), appRegistry["last_loaded_at"])
	}
	if appRegistry["last_updated_at"] != loadedAt.Format(time.RFC3339) {
		t.Errorf("app_registry.last_updated_at: want %s, got %v",
			loadedAt.Format(time.RFC3339), appRegistry["last_updated_at"])
	}
}

func TestStatus_AppRegistryFailureIsDegradedButReady(t *testing.T) {
	st := newFakeStore()
	cache := registry.NewCache()
	cache.MarkLoadFailed(errors.New("console unavailable"))
	srv := newServerWithAPIKey(t, st, "", handler.WithAppRegistry(cache))
	defer srv.Close()

	readyResp := get(t, srv, "/readyz")
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("readyz should stay ready for registry-only degradation, got %d", readyResp.StatusCode)
	}

	resp := get(t, srv, "/api/v1/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["status"] != "degraded" {
		t.Errorf("status: want degraded, got %v", body["status"])
	}
	if body["is_degraded"] != true {
		t.Errorf("is_degraded: want true, got %v", body["is_degraded"])
	}
	if !jsonArrayContains(body["degraded_components"], "app_registry") {
		t.Errorf("degraded_components missing app_registry: %v", body["degraded_components"])
	}
	appRegistry, ok := body["app_registry"].(map[string]any)
	if !ok {
		t.Fatalf("app_registry missing from /api/v1/status response: %#v", body["app_registry"])
	}
	if appRegistry["status"] != "degraded" {
		t.Errorf("app_registry.status: want degraded, got %v", appRegistry["status"])
	}
	if !strings.Contains(appRegistry["last_load_error"].(string), "console unavailable") {
		t.Errorf("app_registry.last_load_error: got %v", appRegistry["last_load_error"])
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
	if !jsonArrayContains(body["degraded_components"], "store") {
		t.Errorf("degraded_components missing store: %v", body["degraded_components"])
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

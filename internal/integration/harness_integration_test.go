//go:build integration

// Tests in this file must NOT call t.Parallel: metrics.ResetForTest mutates a
// package-level registry and sequential execution is required for isolation.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/aap/config-server/internal/gitops"
	"github.com/aap/config-server/internal/handler"
	"github.com/aap/config-server/internal/metrics"
	"github.com/aap/config-server/internal/registry"
	"github.com/aap/config-server/internal/secret"
	"github.com/aap/config-server/internal/store"
)

// ─── helpers ────────────────────────────────────────────────────────────────

// newLocalGitRepo creates a bare "remote" repo seeded with the provided files,
// clones it into a temp dir, and returns the remotePath plus a gitops.Repo.
// The remote is local-filesystem only; no network is required.
//
// Branch is "master" because gogit.PlainInit defaults to that name. If go-git
// ever changes its default, update Branch here and in gitops.New accordingly.
func newLocalGitRepo(t *testing.T, files map[string][]byte) (remotePath string, repo *gitops.Repo) {
	t.Helper()

	seedPath := t.TempDir()
	seedRepo, err := gogit.PlainInit(seedPath, false)
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	wt, err := seedRepo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}

	for rel, data := range files {
		full := filepath.Join(seedPath, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		if _, err := wt.Add(rel); err != nil {
			t.Fatalf("git add %s: %v", rel, err)
		}
	}
	if _, err := wt.Commit("seed", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	remotePath = t.TempDir()
	if _, err := gogit.PlainInit(remotePath, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	if _, err := seedRepo.CreateRemote(&gogitcfg.RemoteConfig{
		Name: "origin",
		URLs: []string{remotePath},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	if err := seedRepo.Push(&gogit.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("seed push: %v", err)
	}

	clonePath := t.TempDir()
	repo, err = gitops.New(gitops.Options{
		LocalPath: clonePath,
		RemoteURL: remotePath,
		Branch:    "master", // gogit default branch
	})
	if err != nil {
		t.Fatalf("gitops.New: %v", err)
	}
	return remotePath, repo
}

// pushOutOfBand creates a second clone of remotePath, writes files, commits,
// and pushes — bypassing the Store under test to simulate an external actor.
func pushOutOfBand(t *testing.T, remotePath string, files map[string][]byte, msg string) {
	t.Helper()
	clonePath := t.TempDir()
	r, err := gogit.PlainClone(clonePath, false, &gogit.CloneOptions{
		URL: remotePath,
	})
	if err != nil {
		t.Fatalf("out-of-band clone: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("out-of-band worktree: %v", err)
	}
	for rel, data := range files {
		full := filepath.Join(clonePath, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("out-of-band mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatalf("out-of-band write %s: %v", rel, err)
		}
		if _, err := wt.Add(rel); err != nil {
			t.Fatalf("out-of-band git add %s: %v", rel, err)
		}
	}
	if _, err := wt.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{Name: "oob", Email: "oob@test.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("out-of-band commit: %v", err)
	}
	if err := r.Push(&gogit.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("out-of-band push: %v", err)
	}
}

// fakeReadiness always reports ready.
type fakeReadiness struct{}

func (fakeReadiness) IsReady() bool { return true }

// sealedCall records a Seal invocation with bytes captured before any zeroing.
type sealedCall struct {
	Org, Project, Service, Namespace, Name string
	Data                                   map[string][]byte // copied at Seal time
}

// fakeSealer records seal calls with defensively copied plaintext bytes.
// The handler calls destroySecretWrites (zeroing secret.Value bytes) after
// ApplyChanges returns, so bytes must be copied inside Seal while they are
// still live.
type fakeSealer struct {
	calls []sealedCall
}

func (s *fakeSealer) Seal(_ context.Context, req secret.SealRequest) (secret.SealedManifest, error) {
	data := make(map[string][]byte, len(req.Data))
	for k, v := range req.Data {
		data[k] = v.Bytes() // defensive copy before destroySecretWrites runs
	}
	s.calls = append(s.calls, sealedCall{
		Org:       req.Org,
		Project:   req.Project,
		Service:   req.Service,
		Namespace: req.Namespace,
		Name:      req.Name,
		Data:      data,
	})
	return secret.SealedManifest{
		Namespace: req.Namespace,
		Name:      req.Name,
		Path: store.ServicePath(req.Org, req.Project, req.Service) +
			"/sealed-secrets/" + req.Namespace + "/" + req.Name + ".yaml",
		YAML: []byte("fake-sealed-" + req.Name),
	}, nil
}

// fakeApplier records manifests that were applied to Kubernetes.
type fakeApplier struct {
	applied []secret.SealedManifest
}

func (a *fakeApplier) ApplySealedSecret(_ context.Context, m secret.SealedManifest) error {
	a.applied = append(a.applied, m)
	return nil
}

// fakeConsoleServer returns an httptest.Server that serves a static App
// Registry JSON payload at GET /api/v1/apps?all=true.
func fakeConsoleServer(t *testing.T, apps []registry.App) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Query().Get("all") != "true" {
			http.Error(w, "missing all=true", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(apps); err != nil {
			t.Errorf("encode apps: %v", err)
		}
	}))
}

// doJSON sends a JSON request with the test API key set on every request.
func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, err := http.NewRequest(method, srv.URL+path, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-API-Key", "test-api-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// seedFiles returns an initial set of config-repo files for one service using
// the schema expected by internal/parser (envelope keys: version, metadata,
// config / env_vars).
func seedFiles(org, project, svc string) map[string][]byte {
	base := store.ServicePath(org, project, svc)
	configYAML := fmt.Sprintf(`version: "1"
metadata:
  org: %s
  project: %s
  service: %s
config:
  app_port: 8080
  debug: false
`, org, project, svc)
	envVarsYAML := fmt.Sprintf(`version: "1"
metadata:
  org: %s
  project: %s
  service: %s
env_vars:
  plain:
    LOG_LEVEL: info
    REGION: us-east-1
`, org, project, svc)
	return map[string][]byte{
		base + "/config.yaml":   []byte(configYAML),
		base + "/env_vars.yaml": []byte(envVarsYAML),
	}
}

// ─── scenarios ───────────────────────────────────────────────────────────────

// TestIntegration_StartupLoadFromGit verifies that a Store boots from a
// fake-local Git repo and serves config via the HTTP handler without any live
// network or cluster dependency.
func TestIntegration_StartupLoadFromGit(t *testing.T) {
	metrics.ResetForTest()
	ctx := context.Background()

	const org, project, svc = "testorg", "testproj", "testsvc"
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))

	st := store.New(repo)
	if err := st.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	mux := http.NewServeMux()
	h := handler.New(st, fakeReadiness{}, "test-api-key")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp := doJSON(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/orgs/%s/projects/%s/services/%s/config", org, project, svc),
		nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET config: got %d, want 200", resp.StatusCode)
	}

	// Response shape: {"metadata": {...}, "config": {"app_port": 8080, ...}}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode config response: %v", err)
	}
	cfgSection, ok := body["config"].(map[string]any)
	if !ok {
		t.Fatalf("config key missing or not an object: %v", body)
	}
	if port, _ := cfgSection["app_port"].(float64); int(port) != 8080 {
		t.Errorf("app_port = %v, want 8080", cfgSection["app_port"])
	}
}

// TestIntegration_ConsoleRegistryBootstrap verifies that Bootstrap loads the
// fake Console app list into the registry cache and the status endpoint
// reflects the loaded state (apps_loaded count and status "ok").
func TestIntegration_ConsoleRegistryBootstrap(t *testing.T) {
	metrics.ResetForTest()
	ctx := context.Background()

	apps := []registry.App{
		{Org: "testorg", Project: "testproj", Service: "svc1"},
		{Org: "testorg", Project: "testproj", Service: "svc2"},
	}
	consoleSrv := fakeConsoleServer(t, apps)
	t.Cleanup(consoleSrv.Close)

	client, err := registry.NewConsoleClient(registry.ClientOptions{BaseURL: consoleSrv.URL})
	if err != nil {
		t.Fatalf("new console client: %v", err)
	}
	cache := registry.NewCache()
	result := registry.Bootstrap(ctx, cache, client, registry.BootstrapOptions{
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
	})
	if !result.Loaded {
		t.Fatalf("registry bootstrap failed: %v", result.Err)
	}
	if result.AppsLoaded != len(apps) {
		t.Errorf("AppsLoaded = %d, want %d", result.AppsLoaded, len(apps))
	}

	// Verify the loaded registry surfaces through the handler's /api/v1/status.
	const org, project, svc = "testorg", "testproj", "svc1"
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))
	st := store.New(repo)
	if err := st.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	mux := http.NewServeMux()
	h := handler.New(st, fakeReadiness{}, "test-api-key", handler.WithAppRegistry(cache))
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp := doJSON(t, srv, http.MethodGet, "/api/v1/status", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/status: got %d, want 200", resp.StatusCode)
	}

	var statusBody map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&statusBody); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	reg, ok := statusBody["app_registry"].(map[string]any)
	if !ok {
		t.Fatalf("app_registry missing in status: %v", statusBody)
	}
	if loaded, _ := reg["apps_loaded"].(float64); int(loaded) != len(apps) {
		t.Errorf("apps_loaded = %v, want %d", reg["apps_loaded"], len(apps))
	}
	if reg["status"] != "ok" {
		t.Errorf("registry status = %v, want ok", reg["status"])
	}
}

// TestIntegration_AdminWriteAndReload verifies the full config-write path:
// handler → store.ApplyChanges → fake Git commit → in-memory snapshot reload →
// subsequent read reflects the new value.
func TestIntegration_AdminWriteAndReload(t *testing.T) {
	metrics.ResetForTest()
	ctx := context.Background()

	const org, project, svc = "testorg", "testproj", "writesvc"
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))

	st := store.New(repo)
	if err := st.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	mux := http.NewServeMux()
	h := handler.New(st, fakeReadiness{}, "test-api-key")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	changeBody := map[string]any{
		"org":     org,
		"project": project,
		"service": svc,
		"config":  map[string]any{"app_port": 9090, "feature_x": true},
		"message": "integration test write",
	}
	resp := doJSON(t, srv, http.MethodPost, "/api/v1/admin/changes", changeBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST admin/changes: got %d — %s", resp.StatusCode, body)
	}

	// Read back — must reflect the update.
	getResp := doJSON(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/orgs/%s/projects/%s/services/%s/config", org, project, svc),
		nil)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET config after write: got %d", getResp.StatusCode)
	}

	// Response shape: {"metadata": {...}, "config": {"app_port": 9090, ...}}
	var respBody map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&respBody); err != nil {
		t.Fatalf("decode config after write: %v", err)
	}
	cfgSection, ok := respBody["config"].(map[string]any)
	if !ok {
		t.Fatalf("config key missing or not an object: %v", respBody)
	}
	if port, _ := cfgSection["app_port"].(float64); int(port) != 9090 {
		t.Errorf("app_port = %v, want 9090", cfgSection["app_port"])
	}
}

// TestIntegration_AdminEnvVarsWriteAndReload verifies that the env_vars write
// path flows through the full HTTP handler → store → Git commit → reload chain
// and the subsequent read reflects the new plain env var value.
func TestIntegration_AdminEnvVarsWriteAndReload(t *testing.T) {
	metrics.ResetForTest()
	ctx := context.Background()

	const org, project, svc = "testorg", "testproj", "envsvc"
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))

	st := store.New(repo)
	if err := st.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	mux := http.NewServeMux()
	h := handler.New(st, fakeReadiness{}, "test-api-key")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	changeBody := map[string]any{
		"org":     org,
		"project": project,
		"service": svc,
		"env_vars": map[string]any{
			"plain": map[string]string{
				"LOG_LEVEL": "debug",
				"REGION":    "eu-west-1",
			},
		},
		"message": "integration test env_vars write",
	}
	resp := doJSON(t, srv, http.MethodPost, "/api/v1/admin/changes", changeBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST admin/changes (env_vars): got %d — %s", resp.StatusCode, body)
	}

	// Read back the env_vars — must reflect the updated values.
	getResp := doJSON(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/orgs/%s/projects/%s/services/%s/env_vars", org, project, svc),
		nil)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET env_vars after write: got %d", getResp.StatusCode)
	}

	// Response shape: {"metadata": {...}, "env_vars": {"plain": {...}, "secret_refs": {...}}}
	var respBody map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&respBody); err != nil {
		t.Fatalf("decode env_vars after write: %v", err)
	}
	evSection, ok := respBody["env_vars"].(map[string]any)
	if !ok {
		t.Fatalf("env_vars key missing or not an object: %v", respBody)
	}
	plain, ok := evSection["plain"].(map[string]any)
	if !ok {
		t.Fatalf("env_vars.plain missing: %v", evSection)
	}
	if plain["LOG_LEVEL"] != "debug" {
		t.Errorf("LOG_LEVEL = %v, want debug", plain["LOG_LEVEL"])
	}
	if plain["REGION"] != "eu-west-1" {
		t.Errorf("REGION = %v, want eu-west-1", plain["REGION"])
	}
}

// TestIntegration_SecretWriteWithFakeAdapters verifies that a secret write
// flows through the fake Sealer and Applier without touching a real cluster,
// and that the correct namespace, name, and plaintext key are sealed.
func TestIntegration_SecretWriteWithFakeAdapters(t *testing.T) {
	metrics.ResetForTest()
	ctx := context.Background()

	const org, project, svc = "testorg", "testproj", "secretsvc"
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))

	sealer := &fakeSealer{}
	applier := &fakeApplier{}
	secretDeps := secret.Dependencies{
		Sealer:  sealer,
		Applier: applier,
		Auditor: secret.NoopAuditor{},
	}

	st := store.New(repo, store.WithSecretDependencies(secretDeps))
	if err := st.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	mux := http.NewServeMux()
	h := handler.New(st, fakeReadiness{}, "test-api-key",
		handler.WithSecretDependencies(secretDeps))
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	changeBody := map[string]any{
		"org":     org,
		"project": project,
		"service": svc,
		"secrets": map[string]any{
			"app-secrets": map[string]any{
				"namespace": "default",
				"data": map[string]string{
					"DB_PASSWORD": "s3cr3t",
				},
			},
		},
		"message": "integration test secret write",
	}
	resp := doJSON(t, srv, http.MethodPost, "/api/v1/admin/changes", changeBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST admin/changes with secret: got %d — %s", resp.StatusCode, body)
	}

	if len(sealer.calls) == 0 {
		t.Fatal("expected Sealer.Seal to be called at least once")
	}
	call := sealer.calls[0]
	if call.Namespace != "default" {
		t.Errorf("Seal namespace = %q, want default", call.Namespace)
	}
	if call.Name != "app-secrets" {
		t.Errorf("Seal name = %q, want app-secrets", call.Name)
	}
	if string(call.Data["DB_PASSWORD"]) != "s3cr3t" {
		t.Errorf("Seal DB_PASSWORD = %q, want s3cr3t", call.Data["DB_PASSWORD"])
	}

	if len(applier.applied) == 0 {
		t.Fatal("expected Applier.ApplySealedSecret to be called at least once")
	}
	if applier.applied[0].Namespace != "default" {
		t.Errorf("Apply namespace = %q, want default", applier.applied[0].Namespace)
	}
	if applier.applied[0].Name != "app-secrets" {
		t.Errorf("Apply name = %q, want app-secrets", applier.applied[0].Name)
	}
}

// TestIntegration_SnapshotVisibilityAfterReload confirms that an out-of-band
// push to the bare remote is picked up by ReloadFromRepo and reflected in
// subsequent reads without restarting the server.
func TestIntegration_SnapshotVisibilityAfterReload(t *testing.T) {
	metrics.ResetForTest()
	ctx := context.Background()

	const org, project, svc = "testorg", "testproj", "reloadsvc"
	remotePath, repo := newLocalGitRepo(t, seedFiles(org, project, svc))

	st := store.New(repo)
	if err := st.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}
	v1 := st.HeadVersion()

	// Push a change directly to the bare remote, bypassing the Store.
	newConfigYAML := fmt.Sprintf(`version: "1"
metadata:
  org: %s
  project: %s
  service: %s
config:
  app_port: 7070
`, org, project, svc)
	pushOutOfBand(t, remotePath, map[string][]byte{
		store.ServicePath(org, project, svc) + "/config.yaml": []byte(newConfigYAML),
	}, "out-of-band port change")

	// ReloadFromRepo must discover the new commit pushed outside the Store.
	updated, err := st.ReloadFromRepo(ctx)
	if err != nil {
		t.Fatalf("ReloadFromRepo: %v", err)
	}
	if !updated {
		t.Errorf("ReloadFromRepo updated=false, want true (remote HEAD advanced)")
	}
	v2 := st.HeadVersion()
	if v1 == v2 {
		t.Errorf("HeadVersion unchanged after out-of-band push: %s", v1)
	}

	mux := http.NewServeMux()
	h := handler.New(st, fakeReadiness{}, "test-api-key")
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp := doJSON(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/orgs/%s/projects/%s/services/%s/config", org, project, svc),
		nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET config after reload: got %d", resp.StatusCode)
	}

	var respBody map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfgSection, ok := respBody["config"].(map[string]any)
	if !ok {
		t.Fatalf("config key missing or not an object: %v", respBody)
	}
	if port, _ := cfgSection["app_port"].(float64); int(port) != 7070 {
		t.Errorf("app_port = %v, want 7070", cfgSection["app_port"])
	}
}

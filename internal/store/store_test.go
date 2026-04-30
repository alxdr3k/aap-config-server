package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aap/config-server/internal/apperror"
	"github.com/aap/config-server/internal/gitops"
	"github.com/aap/config-server/internal/metrics"
	"github.com/aap/config-server/internal/parser"
	"github.com/aap/config-server/internal/secret"
	"github.com/aap/config-server/internal/store"
)

// fakeRepo is a minimal in-memory GitRepo for testing the store in isolation.
// All state is guarded by mu so concurrent tests exercise the store's locking
// without racing on the fake itself.
type fakeRepo struct {
	mu              sync.Mutex
	files           map[string][]byte
	filesAtCommit   map[string]map[string][]byte
	commitHash      string
	nextPullUpdated bool
	pullCalls       int
	afterPull       func(*fakeRepo)
	afterCommit     func()
	history         []gitops.ServiceHistoryEntry
}

type fakeSealer struct {
	requests []secret.SealRequest
	err      error
}

func (f *fakeSealer) Seal(_ context.Context, req secret.SealRequest) (secret.SealedManifest, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return secret.SealedManifest{}, f.err
	}
	return secret.SealedManifest{
		Namespace: req.Namespace,
		Name:      req.Name,
		Path:      store.ServicePath(req.Org, req.Project, req.Service) + "/sealed-secrets/" + req.Namespace + "/" + req.Name + ".yaml",
		YAML:      []byte("sealed-" + req.Name),
	}, nil
}

type fakeApplier struct {
	manifests []secret.SealedManifest
	err       error
	ctxErr    error
}

func (f *fakeApplier) ApplySealedSecret(ctx context.Context, manifest secret.SealedManifest) error {
	f.manifests = append(f.manifests, manifest)
	f.ctxErr = ctx.Err()
	return f.err
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		files:         map[string][]byte{},
		filesAtCommit: map[string]map[string][]byte{},
		commitHash:    "abc123",
	}
}

func cloneFakeFiles(files map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for path, data := range files {
		out[path] = append([]byte(nil), data...)
	}
	return out
}

func (f *fakeRepo) CloneOrOpen(_ context.Context) error { return nil }

func (f *fakeRepo) Pull(_ context.Context) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pullCalls++
	updated := f.nextPullUpdated
	f.nextPullUpdated = false
	if updated && f.afterPull != nil {
		f.afterPull(f)
	}
	return f.commitHash, updated, nil
}

func (f *fakeRepo) CommitAndPush(_ context.Context, _ string, files map[string][]byte) (string, error) {
	return f.CommitAndPushFunc(context.Background(), "", func(gitops.FileReader) (map[string][]byte, error) {
		return files, nil
	})
}

func (f *fakeRepo) CommitAndPushFunc(_ context.Context, _ string, build gitops.CommitFileBuilder) (string, error) {
	f.mu.Lock()
	snap := make(map[string][]byte, len(f.files))
	for k, v := range f.files {
		snap[k] = append([]byte(nil), v...)
	}
	f.mu.Unlock()

	files, err := build(mapFileReader{files: snap})
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range files {
		f.files[k] = v
	}
	f.commitHash = "newcommit"
	if f.afterCommit != nil {
		f.afterCommit()
	}
	return f.commitHash, nil
}

func (f *fakeRepo) DeleteAndPush(_ context.Context, _ string, paths []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range paths {
		for path := range f.files {
			if path == p || strings.HasPrefix(path, p+"/") {
				delete(f.files, path)
			}
		}
	}
	f.commitHash = "delcommit"
	return f.commitHash, nil
}

func (f *fakeRepo) RestoreServiceFilesAndPush(
	_ context.Context,
	_ string,
	org, project, service string,
	files map[string][]byte,
) (string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	target := make(map[string][]byte, len(files))
	for path, data := range files {
		target[filepath.ToSlash(path)] = append([]byte(nil), data...)
	}
	var deleted []string
	for path := range f.files {
		change, ok := gitops.ClassifyServiceFileChange(path, org, project, service)
		if !ok {
			continue
		}
		if _, keep := target[path]; keep {
			continue
		}
		deleted = append(deleted, change.Path)
		delete(f.files, path)
	}
	sort.Strings(deleted)
	for path, data := range target {
		f.files[path] = append([]byte(nil), data...)
	}
	f.commitHash = "revertcommit"
	return f.commitHash, deleted, nil
}

func (f *fakeRepo) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %w", os.ErrNotExist)
	}
	return d, nil
}

type mapFileReader struct {
	files map[string][]byte
}

func (r mapFileReader) ReadFile(path string) ([]byte, error) {
	data, ok := r.files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %w", os.ErrNotExist)
	}
	return append([]byte(nil), data...), nil
}

type versionWaitResult struct {
	version string
	changed bool
	err     error
}

func waitForVersionChangeAsync(ctx context.Context, s *store.Store, version string) <-chan versionWaitResult {
	ch := make(chan versionWaitResult, 1)
	go func() {
		next, changed, err := s.WaitForVersionChange(ctx, version)
		ch <- versionWaitResult{version: next, changed: changed, err: err}
	}()
	return ch
}

func receiveWaitResult(t *testing.T, ch <-chan versionWaitResult) versionWaitResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WaitForVersionChange")
		return versionWaitResult{}
	}
}

// WalkConfigs copies the file map under the lock, then iterates the copy so
// the caller's fn runs without holding the lock (matches the real Repo, which
// holds its own lock for the walk but doesn't hold it while re-entering via
// external callbacks).
func (f *fakeRepo) WalkConfigs(fn func(path string, data []byte) error) error {
	f.mu.Lock()
	snap := make(map[string][]byte, len(f.files))
	for k, v := range f.files {
		snap[k] = v
	}
	f.mu.Unlock()
	for path, data := range snap {
		if err := fn(path, data); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot mirrors gitops.Repo.Snapshot: HEAD + walk are observed together.
func (f *fakeRepo) Snapshot(fn func(path string, data []byte) error) (string, error) {
	f.mu.Lock()
	hash := f.commitHash
	snap := make(map[string][]byte, len(f.files))
	for k, v := range f.files {
		snap[k] = v
	}
	f.mu.Unlock()
	for path, data := range snap {
		if err := fn(path, data); err != nil {
			return "", err
		}
	}
	return hash, nil
}

func (f *fakeRepo) HeadHash() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commitHash, nil
}

func (f *fakeRepo) ReadFileAtCommit(commit string, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	files := f.files
	if snap, ok := f.filesAtCommit[commit]; ok {
		files = snap
	} else if commit != f.commitHash {
		return nil, fmt.Errorf("%w: commit %s", gitops.ErrCommitNotFound, commit)
	}
	d, ok := files[path]
	if !ok {
		return nil, fmt.Errorf("%w: file %s at %s", gitops.ErrFileNotFoundAtCommit, path, commit)
	}
	return append([]byte(nil), d...), nil
}

func (f *fakeRepo) ReadServiceFilesAtCommit(
	_ context.Context,
	commit, org, project, service string,
) ([]gitops.ServiceFileContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	files := f.files
	if snap, ok := f.filesAtCommit[commit]; ok {
		files = snap
	} else if commit != f.commitHash {
		return nil, fmt.Errorf("%w: commit %s", gitops.ErrCommitNotFound, commit)
	}
	var out []gitops.ServiceFileContent
	for path, data := range files {
		change, ok := gitops.ClassifyServiceFileChange(path, org, project, service)
		if !ok {
			continue
		}
		out = append(out, gitops.ServiceFileContent{
			Path: change.Path,
			Kind: change.Kind,
			Data: append([]byte(nil), data...),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (f *fakeRepo) IterateServiceHistory(_ context.Context, _, _, _ string, fn func(gitops.ServiceHistoryEntry) error) error {
	for _, entry := range f.history {
		if err := fn(entry); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeRepo) LocalPath() string { return "/fake" }

// seedFakeRepo adds a service's config + env_vars files to the fake repo.
func seedFakeRepo(f *fakeRepo, org, project, svc string) {
	base := "configs/orgs/" + org + "/projects/" + project + "/services/" + svc
	f.files[base+"/config.yaml"] = []byte(`version: "1"
metadata:
  service: ` + svc + `
  org: ` + org + `
  project: ` + project + `
  updated_at: "2026-03-09T10:00:00Z"
config:
  router_settings:
    num_retries: 3
`)
	f.files[base+"/env_vars.yaml"] = []byte(`version: "1"
metadata:
  service: ` + svc + `
  org: ` + org + `
  project: ` + project + `
env_vars:
  plain:
    LOG_LEVEL: "INFO"
  secret_refs:
    API_KEY: "my-api-key"
`)
}

func seedSecretFiles(f *fakeRepo, org, project, svc string) {
	base := "configs/orgs/" + org + "/projects/" + project + "/services/" + svc
	f.files[base+"/secrets.yaml"] = []byte(`version: "1"
secrets:
  - id: existing-api-key
    description: ""
    k8s_secret:
      name: remote-secrets
      namespace: ai-platform
      key: api-key
`)
	f.files[base+"/sealed-secrets/ai-platform/remote-secrets.yaml"] = []byte("sealed-remote-secrets")
}

func TestStore_GetConfig_NotFound(t *testing.T) {
	ctx := context.Background()
	s := store.New(newFakeRepo())
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	_, err := s.GetConfig(ctx, "no-org", "no-proj", "no-svc")
	if err == nil {
		t.Fatal("expected not-found error")
	}

	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T", err)
	}
	if appErr.Code != apperror.CodeNotFound {
		t.Errorf("expected CodeNotFound, got %q", appErr.Code)
	}
}

func TestStore_GetConfig_Found(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	d, err := s.GetConfig(ctx, "myorg", "proj", "litellm")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if d.Config == nil {
		t.Fatal("expected non-nil Config")
	}
	if d.Config.Metadata.Service != "litellm" {
		t.Errorf("service: want %q, got %q", "litellm", d.Config.Metadata.Service)
	}
	if d.EnvVars == nil {
		t.Fatal("expected non-nil EnvVars")
	}
	if d.EnvVars.EnvVars.Plain["LOG_LEVEL"] != "INFO" {
		t.Errorf("LOG_LEVEL: want INFO, got %q", d.EnvVars.EnvVars.Plain["LOG_LEVEL"])
	}
}

func TestStore_LoadFromRepo_ParsesDefaultsSources(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	repo.files["configs/_defaults/common.yaml"] = []byte(`config:
  global_only: true
env_vars:
  plain:
    GLOBAL_ENV: "1"
`)
	repo.files["configs/orgs/myorg/_defaults/common.yaml"] = []byte(`env_vars:
  secret_refs:
    ORG_SECRET: org-secret
`)
	repo.files["configs/orgs/myorg/projects/proj/_defaults/common.yaml"] = []byte(`config:
  project_only: true
`)
	repo.files["configs/orgs/other/_defaults/common.yaml"] = []byte(`config:
  other_org: true
`)

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	d, err := s.GetConfig(ctx, "myorg", "proj", "litellm")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if len(d.InheritedSources) != 3 {
		t.Fatalf("InheritedSources: got %+v", d.InheritedSources)
	}
	want := []struct {
		scope      store.DefaultsScope
		org        string
		project    string
		path       string
		hasConfig  bool
		hasEnvVars bool
	}{
		{store.DefaultsScopeGlobal, "", "", "configs/_defaults/common.yaml", true, true},
		{store.DefaultsScopeOrg, "myorg", "", "configs/orgs/myorg/_defaults/common.yaml", false, true},
		{store.DefaultsScopeProject, "myorg", "proj", "configs/orgs/myorg/projects/proj/_defaults/common.yaml", true, false},
	}
	for i, w := range want {
		got := d.InheritedSources[i]
		if got.Scope != w.scope ||
			got.Org != w.org ||
			got.Project != w.project ||
			got.Path != w.path ||
			got.HasConfig != w.hasConfig ||
			got.HasEnvVars != w.hasEnvVars {
			t.Fatalf("source %d: got %+v, want %+v", i, got, w)
		}
	}
	if _, ok := d.Config.Config["global_only"]; ok {
		t.Fatal("EXT-1C.1 should expose defaults metadata without merging config values")
	}
}

func TestStore_LoadFromRepo_InvalidDefaultsFailsReload(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	repo.files["configs/_defaults/common.yaml"] = []byte("config: [")

	s := store.New(repo)
	err := s.LoadFromRepo(ctx)
	if err == nil {
		t.Fatal("expected invalid defaults to fail reload")
	}
	if !strings.Contains(err.Error(), "configs/_defaults/common.yaml") {
		t.Fatalf("error should include defaults path, got %v", err)
	}
}

func TestStore_LoadFromRepo_MergesInheritedConfigAndEnvVars(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	base := "configs/orgs/myorg/projects/proj/services/litellm"
	repo.files[base+"/config.yaml"] = []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config:
  scalar: service
  nested:
    override: service
    service_only: true
  model_list:
    - service-model
  delete_service: null
`)
	repo.files[base+"/env_vars.yaml"] = []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
env_vars:
  plain:
    B: service
    D: service
  secret_refs:
    T: service-secret
`)
	repo.files["configs/_defaults/common.yaml"] = []byte(`config:
  scalar: global
  delete_org: keep
  delete_service: keep
  nested:
    keep: global
    override: global
    delete_nested: keep
  model_list:
    - global-model
  project_list:
    - global-project
env_vars:
  plain:
    A: global
    B: global
  secret_refs:
    S: global-secret
`)
	repo.files["configs/orgs/myorg/_defaults/common.yaml"] = []byte(`config:
  scalar: org
  delete_org: null
  nested:
    override: org
    org_only: true
  model_list:
    - org-model
env_vars:
  plain:
    A: org
    C: org
  secret_refs:
    S: org-secret
`)
	repo.files["configs/orgs/myorg/projects/proj/_defaults/common.yaml"] = []byte(`config:
  scalar: project
  nested:
    project_only: true
    delete_nested: null
  project_list:
    - project-model
env_vars:
  plain:
    C: project
`)

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	d, err := s.GetConfig(ctx, "myorg", "proj", "litellm")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if d.InheritedConfig == nil {
		t.Fatal("expected inherited config")
	}
	cfg := d.InheritedConfig.Config
	if got := cfg["scalar"]; got != "service" {
		t.Fatalf("scalar: got %v", got)
	}
	if _, ok := cfg["delete_org"]; ok {
		t.Fatal("delete_org should be removed by org null override")
	}
	if _, ok := cfg["delete_service"]; ok {
		t.Fatal("delete_service should be removed by service null override")
	}
	nested, ok := cfg["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested: got %T", cfg["nested"])
	}
	if got := nested["keep"]; got != "global" {
		t.Fatalf("nested.keep: got %v", got)
	}
	if got := nested["override"]; got != "service" {
		t.Fatalf("nested.override: got %v", got)
	}
	if got := nested["org_only"]; got != true {
		t.Fatalf("nested.org_only: got %v", got)
	}
	if got := nested["project_only"]; got != true {
		t.Fatalf("nested.project_only: got %v", got)
	}
	if got := nested["service_only"]; got != true {
		t.Fatalf("nested.service_only: got %v", got)
	}
	if _, ok := nested["delete_nested"]; ok {
		t.Fatal("nested.delete_nested should be removed by project null override")
	}
	modelList, ok := cfg["model_list"].([]any)
	if !ok || len(modelList) != 1 || modelList[0] != "service-model" {
		t.Fatalf("model_list should be replaced by service array, got %#v", cfg["model_list"])
	}
	projectList, ok := cfg["project_list"].([]any)
	if !ok || len(projectList) != 1 || projectList[0] != "project-model" {
		t.Fatalf("project_list should be replaced by project array, got %#v", cfg["project_list"])
	}

	if _, ok := d.Config.Config["delete_service"]; !ok {
		t.Fatal("raw service config should keep service-level null until inherit reads are enabled")
	}
	if d.InheritedEnvVars == nil {
		t.Fatal("expected inherited env vars")
	}
	if got := d.InheritedEnvVars.EnvVars.Plain; got["A"] != "org" || got["B"] != "service" || got["C"] != "project" || got["D"] != "service" {
		t.Fatalf("merged plain env vars: got %#v", got)
	}
	if got := d.InheritedEnvVars.EnvVars.SecretRefs; got["S"] != "org-secret" || got["T"] != "service-secret" {
		t.Fatalf("merged secret refs: got %#v", got)
	}
}

func TestStore_GetInheritedAtVersion_MergesDefaultsAtCommit(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	repo.filesAtCommit["old"] = map[string][]byte{
		"configs/_defaults/common.yaml": []byte(`config:
  scalar: global
  nested:
    keep: global
env_vars:
  plain:
    A: global
`),
		"configs/orgs/myorg/projects/proj/_defaults/common.yaml": []byte(`config:
  scalar: project
  model_list:
    - project-model
env_vars:
  plain:
    A: project
    B: project
`),
		base + "/config.yaml": []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config:
  scalar: service
  nested:
    service_only: true
`),
		base + "/env_vars.yaml": []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
env_vars:
  plain:
    B: service
    C: service
`),
	}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	config, err := s.GetInheritedConfigAtVersion(ctx, "myorg", "proj", "litellm", "old")
	if err != nil {
		t.Fatalf("GetInheritedConfigAtVersion: %v", err)
	}
	if config.InheritedConfig == nil {
		t.Fatal("expected inherited config")
	}
	if got := config.InheritedConfig.Config["scalar"]; got != "service" {
		t.Fatalf("scalar: got %v", got)
	}
	nested, ok := config.InheritedConfig.Config["nested"].(map[string]any)
	if !ok || nested["keep"] != "global" || nested["service_only"] != true {
		t.Fatalf("nested merge: got %#v", config.InheritedConfig.Config["nested"])
	}
	modelList, ok := config.InheritedConfig.Config["model_list"].([]any)
	if !ok || len(modelList) != 1 || modelList[0] != "project-model" {
		t.Fatalf("historical project array: got %#v", config.InheritedConfig.Config["model_list"])
	}

	envVars, err := s.GetInheritedEnvVarsAtVersion(ctx, "myorg", "proj", "litellm", "old")
	if err != nil {
		t.Fatalf("GetInheritedEnvVarsAtVersion: %v", err)
	}
	if envVars.InheritedEnvVars == nil {
		t.Fatal("expected inherited env vars")
	}
	plain := envVars.InheritedEnvVars.EnvVars.Plain
	if plain["A"] != "project" || plain["B"] != "service" || plain["C"] != "service" {
		t.Fatalf("historical env merge: got %#v", plain)
	}
}

func TestStore_GetConfigAtVersion(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	repo.filesAtCommit["old-config"] = map[string][]byte{
		base + "/config.yaml": []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
  updated_at: "2026-03-01T10:00:00Z"
config:
  router_settings:
    num_retries: 1
`),
	}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	d, err := s.GetConfigAtVersion(ctx, "myorg", "proj", "litellm", "old-config")
	if err != nil {
		t.Fatalf("GetConfigAtVersion: %v", err)
	}
	if d.ConfigResourceVersion != "old-config" {
		t.Fatalf("ConfigResourceVersion: got %q", d.ConfigResourceVersion)
	}
	settings := d.Config.Config["router_settings"].(map[string]any)
	if settings["num_retries"] != 1 {
		t.Fatalf("historical config num_retries: got %#v", settings["num_retries"])
	}
	if got := d.UpdatedAt.Format(time.RFC3339); got != "2026-03-01T10:00:00Z" {
		t.Fatalf("UpdatedAt: got %q", got)
	}
}

func TestStore_GetEnvVarsAtVersion(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	repo.filesAtCommit["old-env"] = map[string][]byte{
		base + "/env_vars.yaml": []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
  updated_at: "2026-03-02T10:00:00Z"
env_vars:
  plain:
    LOG_LEVEL: "DEBUG"
  secret_refs:
    API_KEY: "old-api-key"
`),
	}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	d, err := s.GetEnvVarsAtVersion(ctx, "myorg", "proj", "litellm", "old-env")
	if err != nil {
		t.Fatalf("GetEnvVarsAtVersion: %v", err)
	}
	if d.EnvVarsResourceVersion != "old-env" {
		t.Fatalf("EnvVarsResourceVersion: got %q", d.EnvVarsResourceVersion)
	}
	if d.EnvVars.EnvVars.Plain["LOG_LEVEL"] != "DEBUG" {
		t.Fatalf("historical LOG_LEVEL: got %q", d.EnvVars.EnvVars.Plain["LOG_LEVEL"])
	}
	if d.EnvVars.EnvVars.SecretRefs["API_KEY"] != "old-api-key" {
		t.Fatalf("historical API_KEY ref: got %q", d.EnvVars.EnvVars.SecretRefs["API_KEY"])
	}
}

func TestStore_GetConfigAtVersion_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	_, err := s.GetConfigAtVersion(ctx, "myorg", "proj", "litellm", "missing-version")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %T %v", err, err)
	}
}

func TestStore_GetVersionedResources_EmptyWhenFileMissingAtCommit(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	repo.filesAtCommit["without-files"] = map[string][]byte{}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	config, err := s.GetConfigAtVersion(ctx, "myorg", "proj", "litellm", "without-files")
	if err != nil {
		t.Fatalf("GetConfigAtVersion missing file: %v", err)
	}
	if config.Config != nil || config.ConfigResourceVersion != "without-files" {
		t.Fatalf("missing config file should return empty config data, got %#v", config)
	}

	envVars, err := s.GetEnvVarsAtVersion(ctx, "myorg", "proj", "litellm", "without-files")
	if err != nil {
		t.Fatalf("GetEnvVarsAtVersion missing file: %v", err)
	}
	if envVars.EnvVars != nil || envVars.EnvVarsResourceVersion != "without-files" {
		t.Fatalf("missing env_vars file should return empty env data, got %#v", envVars)
	}
}

func TestStore_PrepareRevert_BuildsRestorePlan(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	seedSecretFiles(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	repo.filesAtCommit["target"] = map[string][]byte{
		base + "/config.yaml": []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config:
  router_settings:
    num_retries: 1
`),
		base + "/sealed-secrets/ai-platform/remote-secrets.yaml": []byte("old-sealed"),
	}
	repo.history = []gitops.ServiceHistoryEntry{{Version: "target"}}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	plan, err := s.PrepareRevert(ctx, &store.RevertRequest{
		Org:           "myorg",
		Project:       "proj",
		Service:       "litellm",
		TargetVersion: "target",
	})
	if err != nil {
		t.Fatalf("PrepareRevert: %v", err)
	}
	if plan.Message != "Rollback to target" {
		t.Fatalf("default message: got %q", plan.Message)
	}
	if plan.Noop {
		t.Fatal("plan should not be noop")
	}
	if got := strings.Join(plan.RestoredFiles, ","); got != "config.yaml,sealed-secrets/ai-platform/remote-secrets.yaml" {
		t.Fatalf("restored files: got %q", got)
	}
	if got := strings.Join(plan.DeletedFiles, ","); got != "env_vars.yaml,secrets.yaml" {
		t.Fatalf("deleted files: got %q", got)
	}
	if string(plan.Files[base+"/sealed-secrets/ai-platform/remote-secrets.yaml"]) != "old-sealed" {
		t.Fatalf("sealed file payload: got %q", plan.Files[base+"/sealed-secrets/ai-platform/remote-secrets.yaml"])
	}
	if _, ok := plan.Files[base+"/env_vars.yaml"]; ok {
		t.Fatal("target-missing env_vars.yaml should not be in restored Files")
	}
}

func TestStore_PrepareRevert_NoopWhenTargetMatchesCurrent(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	repo.history = []gitops.ServiceHistoryEntry{{Version: repo.commitHash}}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	plan, err := s.PrepareRevert(ctx, &store.RevertRequest{
		Org:           "myorg",
		Project:       "proj",
		Service:       "litellm",
		TargetVersion: repo.commitHash,
		Message:       "custom rollback",
	})
	if err != nil {
		t.Fatalf("PrepareRevert: %v", err)
	}
	if !plan.Noop {
		t.Fatal("matching target should be noop")
	}
	if plan.Message != "custom rollback" {
		t.Fatalf("custom message: got %q", plan.Message)
	}
	if len(plan.DeletedFiles) != 0 {
		t.Fatalf("deleted files: got %v", plan.DeletedFiles)
	}
}

func TestStore_PrepareRevert_NotFoundCases(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	repo.filesAtCommit["empty-target"] = map[string][]byte{
		"configs/orgs/myorg/projects/proj/services/other/config.yaml": []byte("version: \"1\"\n"),
	}
	base := store.ServicePath("myorg", "proj", "litellm")
	repo.filesAtCommit["unrelated"] = map[string][]byte{
		base + "/config.yaml": []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config: {}
`),
	}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	tests := []struct {
		name string
		req  *store.RevertRequest
	}{
		{
			name: "missing commit",
			req:  &store.RevertRequest{Org: "myorg", Project: "proj", Service: "litellm", TargetVersion: "missing"},
		},
		{
			name: "no service files at target",
			req:  &store.RevertRequest{Org: "myorg", Project: "proj", Service: "litellm", TargetVersion: "empty-target"},
		},
		{
			name: "target did not change service",
			req:  &store.RevertRequest{Org: "myorg", Project: "proj", Service: "litellm", TargetVersion: "unrelated"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.PrepareRevert(ctx, tc.req)
			if err == nil {
				t.Fatal("expected not-found error")
			}
			var appErr *apperror.Error
			if !errors.As(err, &appErr) || appErr.Code != apperror.CodeNotFound {
				t.Fatalf("expected CodeNotFound, got %T %v", err, err)
			}
		})
	}
}

func TestStore_ApplyRevert_CommitsReloadsAndAppliesSealedSecrets(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	seedSecretFiles(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	repo.filesAtCommit["target"] = map[string][]byte{
		base + "/config.yaml": []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config:
  router_settings:
    num_retries: 1
`),
		base + "/sealed-secrets/ai-platform/remote-secrets.yaml": []byte("old-sealed"),
	}
	repo.history = []gitops.ServiceHistoryEntry{{Version: "target"}}
	applier := &fakeApplier{}

	s := store.New(repo, store.WithSecretDependencies(secret.Dependencies{Applier: applier}))
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	result, err := s.ApplyRevert(ctx, &store.RevertRequest{
		Org:           "myorg",
		Project:       "proj",
		Service:       "litellm",
		TargetVersion: "target",
	})
	if err != nil {
		t.Fatalf("ApplyRevert: %v", err)
	}
	if result.Version != "revertcommit" || result.TargetVersion != "target" {
		t.Fatalf("result versions: %+v", result)
	}
	if result.ApplyFailed || result.ReloadFailed || result.Noop {
		t.Fatalf("unexpected result flags: %+v", result)
	}
	if got := strings.Join(result.DeletedFiles, ","); got != "env_vars.yaml,secrets.yaml" {
		t.Fatalf("deleted files: got %q", got)
	}
	if len(applier.manifests) != 1 {
		t.Fatalf("applied manifests: got %d", len(applier.manifests))
	}
	if applier.manifests[0].Namespace != "ai-platform" || applier.manifests[0].Name != "remote-secrets" {
		t.Fatalf("manifest identity: %+v", applier.manifests[0])
	}
	if string(applier.manifests[0].YAML) != "old-sealed" {
		t.Fatalf("manifest YAML: got %q", applier.manifests[0].YAML)
	}
	if _, err := repo.ReadFile(base + "/env_vars.yaml"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("env_vars.yaml should be deleted, got %v", err)
	}
	d, err := s.GetConfig(ctx, "myorg", "proj", "litellm")
	if err != nil {
		t.Fatalf("GetConfig after revert: %v", err)
	}
	settings := d.Config.Config["router_settings"].(map[string]any)
	if settings["num_retries"] != 1 {
		t.Fatalf("reverted config num_retries: got %#v", settings["num_retries"])
	}
	if d.EnvVars != nil || d.Secrets != nil {
		t.Fatalf("env/secrets should be absent after revert: env=%v secrets=%v", d.EnvVars, d.Secrets)
	}
}

func TestStore_ApplyRevert_NoopDoesNotCommit(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	repo.history = []gitops.ServiceHistoryEntry{{Version: repo.commitHash}}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	result, err := s.ApplyRevert(ctx, &store.RevertRequest{
		Org:           "myorg",
		Project:       "proj",
		Service:       "litellm",
		TargetVersion: repo.commitHash,
	})
	if err != nil {
		t.Fatalf("ApplyRevert: %v", err)
	}
	if !result.Noop || result.Version != "abc123" {
		t.Fatalf("noop result: %+v", result)
	}
	if repo.commitHash != "abc123" {
		t.Fatalf("noop should not commit, head=%q", repo.commitHash)
	}
}

func TestStore_ApplyRevert_NoopPullsBeforeReturning(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	repo.filesAtCommit["target"] = cloneFakeFiles(repo.files)
	repo.history = []gitops.ServiceHistoryEntry{{Version: "target"}}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	repo.nextPullUpdated = true
	repo.afterPull = func(r *fakeRepo) {
		r.commitHash = "remote456"
		r.files[base+"/config.yaml"] = []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config:
  router_settings:
    num_retries: 9
`)
	}

	result, err := s.ApplyRevert(ctx, &store.RevertRequest{
		Org:           "myorg",
		Project:       "proj",
		Service:       "litellm",
		TargetVersion: "target",
	})
	if err != nil {
		t.Fatalf("ApplyRevert: %v", err)
	}
	if result.Noop {
		t.Fatalf("stale no-op should have been re-evaluated after pull: %+v", result)
	}
	if result.Version != "revertcommit" {
		t.Fatalf("expected revert commit after remote changed, got %+v", result)
	}
	if repo.pullCalls < 2 {
		t.Fatalf("expected ApplyRevert to pull before no-op return, pullCalls=%d", repo.pullCalls)
	}
	d, err := s.GetConfig(ctx, "myorg", "proj", "litellm")
	if err != nil {
		t.Fatalf("GetConfig after revert: %v", err)
	}
	settings := d.Config.Config["router_settings"].(map[string]any)
	if settings["num_retries"] != 3 {
		t.Fatalf("revert should restore target after stale no-op pull, got %#v", settings["num_retries"])
	}
}

func TestStore_ApplyRevert_NoopReloadsWhenPullAlreadyUpToDate(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	original := cloneFakeFiles(repo.files)
	repo.filesAtCommit["abc123"] = original
	repo.filesAtCommit["target"] = original
	repo.history = []gitops.ServiceHistoryEntry{{Version: "target"}}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	repo.commitHash = "remote456"
	repo.files[base+"/config.yaml"] = []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config:
  router_settings:
    num_retries: 11
`)

	result, err := s.ApplyRevert(ctx, &store.RevertRequest{
		Org:           "myorg",
		Project:       "proj",
		Service:       "litellm",
		TargetVersion: "target",
	})
	if err != nil {
		t.Fatalf("ApplyRevert: %v", err)
	}
	if result.Noop {
		t.Fatalf("stale snapshot should be reloaded before no-op return: %+v", result)
	}
	if result.Version != "revertcommit" {
		t.Fatalf("expected revert commit after stale snapshot reload, got %+v", result)
	}
	d, err := s.GetConfig(ctx, "myorg", "proj", "litellm")
	if err != nil {
		t.Fatalf("GetConfig after revert: %v", err)
	}
	settings := d.Config.Config["router_settings"].(map[string]any)
	if settings["num_retries"] != 3 {
		t.Fatalf("revert should restore target after force reload, got %#v", settings["num_retries"])
	}
}

func TestStore_ApplyRevert_NoopReloadFailureStillRevertsChangedHead(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	original := cloneFakeFiles(repo.files)
	repo.filesAtCommit["abc123"] = original
	repo.filesAtCommit["target"] = original
	repo.history = []gitops.ServiceHistoryEntry{{Version: "target"}}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	repo.commitHash = "bad456"
	repo.files[base+"/config.yaml"] = []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config: [
`)

	result, err := s.ApplyRevert(ctx, &store.RevertRequest{
		Org:           "myorg",
		Project:       "proj",
		Service:       "litellm",
		TargetVersion: "target",
	})
	if err != nil {
		t.Fatalf("ApplyRevert: %v", err)
	}
	if result.Noop || result.ReloadFailed {
		t.Fatalf("expected rollback to repair bad head, got %+v", result)
	}
	if result.Version != "revertcommit" {
		t.Fatalf("expected revert commit after failed no-op reload, got %+v", result)
	}
	d, err := s.GetConfig(ctx, "myorg", "proj", "litellm")
	if err != nil {
		t.Fatalf("GetConfig after revert: %v", err)
	}
	settings := d.Config.Config["router_settings"].(map[string]any)
	if settings["num_retries"] != 3 {
		t.Fatalf("revert should restore target after failed no-op reload, got %#v", settings["num_retries"])
	}
}

func TestStore_ApplyRevert_RequiresApplierForSealedSecrets(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	repo.filesAtCommit["target"] = map[string][]byte{
		base + "/config.yaml": []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config: {}
`),
		base + "/sealed-secrets/ai-platform/remote-secrets.yaml": []byte("old-sealed"),
	}
	repo.history = []gitops.ServiceHistoryEntry{{Version: "target"}}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	_, err := s.ApplyRevert(ctx, &store.RevertRequest{
		Org:           "myorg",
		Project:       "proj",
		Service:       "litellm",
		TargetVersion: "target",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.CodeValidation {
		t.Fatalf("expected validation error, got %T %v", err, err)
	}
	if repo.commitHash != "abc123" {
		t.Fatalf("validation failure should not commit, head=%q", repo.commitHash)
	}
}

func TestStore_ApplyRevert_AppliesNestedSealedSecretPath(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	nestedPath := base + "/sealed-secrets/archive/ai-platform/remote-secrets.yaml"
	repo.filesAtCommit["target"] = map[string][]byte{
		base + "/config.yaml": []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config: {}
`),
		nestedPath: []byte(`apiVersion: bitnami.com/v1alpha1
kind: SealedSecret
metadata:
  name: remote-secrets
  namespace: ai-platform
spec: {}
`),
	}
	repo.history = []gitops.ServiceHistoryEntry{{Version: "target"}}
	applier := &fakeApplier{}

	s := store.New(repo, store.WithSecretDependencies(secret.Dependencies{Applier: applier}))
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	result, err := s.ApplyRevert(ctx, &store.RevertRequest{
		Org:           "myorg",
		Project:       "proj",
		Service:       "litellm",
		TargetVersion: "target",
	})
	if err != nil {
		t.Fatalf("ApplyRevert: %v", err)
	}
	if result.ApplyFailed {
		t.Fatalf("nested sealed secret should be applied: %+v", result)
	}
	if len(applier.manifests) != 1 {
		t.Fatalf("applied manifests: got %d", len(applier.manifests))
	}
	got := applier.manifests[0]
	if got.Namespace != "ai-platform" || got.Name != "remote-secrets" || got.Path != nestedPath {
		t.Fatalf("manifest identity/path: %+v", got)
	}
}

func TestStore_ApplyRevert_FillsPartialSealedSecretMetadataFromPath(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	base := store.ServicePath("myorg", "proj", "litellm")
	sealedPath := base + "/sealed-secrets/ai-platform/remote-secrets.yaml"
	repo.filesAtCommit["target"] = map[string][]byte{
		base + "/config.yaml": []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config: {}
`),
		sealedPath: []byte(`apiVersion: bitnami.com/v1alpha1
kind: SealedSecret
metadata:
  name: remote-secrets
spec: {}
`),
	}
	repo.history = []gitops.ServiceHistoryEntry{{Version: "target"}}
	applier := &fakeApplier{}

	s := store.New(repo, store.WithSecretDependencies(secret.Dependencies{Applier: applier}))
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	_, err := s.ApplyRevert(ctx, &store.RevertRequest{
		Org:           "myorg",
		Project:       "proj",
		Service:       "litellm",
		TargetVersion: "target",
	})
	if err != nil {
		t.Fatalf("ApplyRevert: %v", err)
	}
	if len(applier.manifests) != 1 {
		t.Fatalf("applied manifests: got %d", len(applier.manifests))
	}
	got := applier.manifests[0]
	if got.Namespace != "ai-platform" || got.Name != "remote-secrets" {
		t.Fatalf("manifest identity: %+v", got)
	}
}

func TestStore_History_FiltersLimitAndBefore(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	repo.history = []gitops.ServiceHistoryEntry{
		{
			Version:   "v4",
			Message:   "latest config",
			Author:    "admin@example.com",
			Timestamp: time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC),
			FilesChanged: []gitops.ServiceFileChange{
				{Path: "config.yaml", Kind: gitops.ServiceFileConfig},
			},
		},
		{
			Version:   "v3",
			Message:   "env only",
			Author:    "admin@example.com",
			Timestamp: time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC),
			FilesChanged: []gitops.ServiceFileChange{
				{Path: "env_vars.yaml", Kind: gitops.ServiceFileEnvVars},
			},
		},
		{
			Version:   "v2",
			Message:   "config and secret",
			Author:    "admin@example.com",
			Timestamp: time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC),
			FilesChanged: []gitops.ServiceFileChange{
				{Path: "config.yaml", Kind: gitops.ServiceFileConfig},
				{Path: "sealed-secrets/ns/name.yaml", Kind: gitops.ServiceFileSealedSecret},
			},
		},
		{
			Version:   "v1",
			Message:   "initial config",
			Author:    "admin@example.com",
			Timestamp: time.Date(2026, 3, 9, 10, 0, 0, 0, time.UTC),
			FilesChanged: []gitops.ServiceFileChange{
				{Path: "config.yaml", Kind: gitops.ServiceFileConfig},
			},
		},
	}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	entries, err := s.History(ctx, store.HistoryOptions{
		Org:     "myorg",
		Project: "proj",
		Service: "litellm",
		File:    "config",
		Limit:   2,
		Before:  "v3",
	})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("history length: want 2, got %d: %#v", len(entries), entries)
	}
	if entries[0].Version != "v2" || entries[1].Version != "v1" {
		t.Fatalf("versions: want [v2 v1], got [%s %s]", entries[0].Version, entries[1].Version)
	}
	if got := strings.Join(entries[0].FilesChanged, ","); got != "config.yaml,sealed-secrets/ns/name.yaml" {
		t.Fatalf("files_changed: got %q", got)
	}

	secretEntries, err := s.History(ctx, store.HistoryOptions{
		Org:     "myorg",
		Project: "proj",
		Service: "litellm",
		File:    "secrets",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("History secrets: %v", err)
	}
	if len(secretEntries) != 1 || secretEntries[0].Version != "v2" {
		t.Fatalf("secret history: want [v2], got %#v", secretEntries)
	}
}

func TestStore_ListOrgsProjectsServices(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "org1", "proj1", "svc1")
	seedFakeRepo(repo, "org1", "proj1", "svc2")
	seedFakeRepo(repo, "org2", "proj2", "svc3")

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	orgs := s.ListOrgs()
	if len(orgs) != 2 {
		t.Errorf("expected 2 orgs, got %d: %v", len(orgs), orgs)
	}

	projects := s.ListProjects("org1")
	if len(projects) != 1 || projects[0] != "proj1" {
		t.Errorf("expected [proj1], got %v", projects)
	}

	services := s.ListServices("org1", "proj1")
	if len(services) != 2 {
		t.Errorf("expected 2 services in org1/proj1, got %d: %v", len(services), services)
	}
}

func TestStore_ApplyChanges(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	result, err := s.ApplyChanges(ctx, &store.ChangeRequest{
		Org:     "myorg",
		Project: "proj",
		Service: "svc",
		Config: map[string]any{
			"router_settings": map[string]any{
				"num_retries": 5,
			},
		},
		Message: "test commit",
	})
	if err != nil {
		t.Fatalf("ApplyChanges: %v", err)
	}
	if result.Version == "" {
		t.Error("expected non-empty version")
	}
	if len(result.Files) == 0 {
		t.Error("expected at least one file written")
	}

	// Verify in-memory store was updated.
	d, err := s.GetConfig(ctx, "myorg", "proj", "svc")
	if err != nil {
		t.Fatalf("GetConfig after ApplyChanges: %v", err)
	}
	if d.Config == nil {
		t.Error("expected Config after apply")
	}
}

func TestStore_ApplyChanges_PreservesServiceLevelWritesWithInheritedDefaults(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "svc")
	defaultsPath := "configs/_defaults/common.yaml"
	repo.files[defaultsPath] = []byte(`config:
  global_only: true
env_vars:
  plain:
    GLOBAL_ENV: "1"
  secret_refs:
    GLOBAL_SECRET: global-secret
`)

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}
	beforeDefaults, err := repo.ReadFile(defaultsPath)
	if err != nil {
		t.Fatalf("read defaults before apply: %v", err)
	}

	result, err := s.ApplyChanges(ctx, &store.ChangeRequest{
		Org:     "myorg",
		Project: "proj",
		Service: "svc",
		Config: map[string]any{
			"service_only": "updated",
		},
		EnvVars: &parser.EnvVars{
			Plain:      map[string]string{"SERVICE_ENV": "updated"},
			SecretRefs: map[string]string{"SERVICE_SECRET": "service-secret"},
		},
		Message: "service-only update",
	})
	if err != nil {
		t.Fatalf("ApplyChanges: %v", err)
	}
	for _, file := range result.Files {
		if strings.Contains(file, "_defaults") {
			t.Fatalf("admin write should not report defaults writes, got %v", result.Files)
		}
	}
	afterDefaults, err := repo.ReadFile(defaultsPath)
	if err != nil {
		t.Fatalf("read defaults after apply: %v", err)
	}
	if !bytes.Equal(beforeDefaults, afterDefaults) {
		t.Fatal("admin write should not mutate _defaults/common.yaml")
	}

	base := store.ServicePath("myorg", "proj", "svc")
	rawConfig, err := repo.ReadFile(base + "/config.yaml")
	if err != nil {
		t.Fatalf("read service config: %v", err)
	}
	parsedConfig, err := parser.ParseConfig(rawConfig)
	if err != nil {
		t.Fatalf("parse service config: %v", err)
	}
	if _, ok := parsedConfig.Config["global_only"]; ok {
		t.Fatalf("service config.yaml should not persist inherited defaults: %#v", parsedConfig.Config)
	}
	if parsedConfig.Config["service_only"] != "updated" {
		t.Fatalf("service config.yaml should persist request config only, got %#v", parsedConfig.Config)
	}

	rawEnv, err := repo.ReadFile(base + "/env_vars.yaml")
	if err != nil {
		t.Fatalf("read service env_vars: %v", err)
	}
	parsedEnv, err := parser.ParseEnvVars(rawEnv)
	if err != nil {
		t.Fatalf("parse service env_vars: %v", err)
	}
	if _, ok := parsedEnv.EnvVars.Plain["GLOBAL_ENV"]; ok {
		t.Fatalf("service env_vars.yaml should not persist inherited plain env: %#v", parsedEnv.EnvVars.Plain)
	}
	if _, ok := parsedEnv.EnvVars.SecretRefs["GLOBAL_SECRET"]; ok {
		t.Fatalf("service env_vars.yaml should not persist inherited secret_refs: %#v", parsedEnv.EnvVars.SecretRefs)
	}
	if parsedEnv.EnvVars.Plain["SERVICE_ENV"] != "updated" {
		t.Fatalf("service env_vars.yaml should persist request env only, got %#v", parsedEnv.EnvVars.Plain)
	}

	d, err := s.GetConfig(ctx, "myorg", "proj", "svc")
	if err != nil {
		t.Fatalf("GetConfig after ApplyChanges: %v", err)
	}
	if _, ok := d.Config.Config["global_only"]; ok {
		t.Fatalf("raw service config should remain service-level only: %#v", d.Config.Config)
	}
	if d.InheritedConfig.Config["global_only"] != true || d.InheritedConfig.Config["service_only"] != "updated" {
		t.Fatalf("inherited config should combine defaults and service config, got %#v", d.InheritedConfig.Config)
	}
	if _, ok := d.EnvVars.EnvVars.Plain["GLOBAL_ENV"]; ok {
		t.Fatalf("raw service env should remain service-level only: %#v", d.EnvVars.EnvVars.Plain)
	}
	if d.InheritedEnvVars.EnvVars.Plain["GLOBAL_ENV"] != "1" ||
		d.InheritedEnvVars.EnvVars.Plain["SERVICE_ENV"] != "updated" {
		t.Fatalf("inherited env should combine defaults and service env, got %#v", d.InheritedEnvVars.EnvVars.Plain)
	}
}

func TestStore_ApplyChanges_WritesAndAppliesSecrets(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "svc")
	sealer := &fakeSealer{}
	applier := &fakeApplier{}
	var auditLogs bytes.Buffer
	s := store.New(repo, store.WithSecretDependencies(secret.Dependencies{
		Sealer:  sealer,
		Applier: applier,
		Auditor: secret.NewSlogAuditorWithLogger(true,
			slog.New(slog.NewJSONHandler(&auditLogs, nil))),
	}))
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	result, err := s.ApplyChanges(ctx, &store.ChangeRequest{
		Org:     "myorg",
		Project: "proj",
		Service: "svc",
		Secrets: map[string]store.SecretWrite{
			"litellm-secrets": {
				Namespace: "ai-platform",
				Data: map[string]secret.Value{
					"master-key":   secret.NewValue([]byte("top-secret")),
					"database-url": secret.NewValue([]byte("postgres://secret")),
				},
			},
		},
		Message: "write secrets",
	})
	if err != nil {
		t.Fatalf("ApplyChanges secrets: %v", err)
	}

	wantFiles := []string{
		"secrets.yaml",
		"sealed-secrets/ai-platform/litellm-secrets.yaml",
	}
	if fmt.Sprint(result.Files) != fmt.Sprint(wantFiles) {
		t.Fatalf("written files: got %v want %v", result.Files, wantFiles)
	}
	if len(sealer.requests) != 1 {
		t.Fatalf("sealer calls: got %d", len(sealer.requests))
	}
	if sealer.requests[0].Namespace != "ai-platform" || sealer.requests[0].Name != "litellm-secrets" {
		t.Fatalf("seal target: %+v", sealer.requests[0])
	}
	if len(applier.manifests) != 1 {
		t.Fatalf("applier calls: got %d", len(applier.manifests))
	}

	secretsPath := "configs/orgs/myorg/projects/proj/services/svc/secrets.yaml"
	secretsYAML := string(repo.files[secretsPath])
	if !strings.Contains(secretsYAML, `id: database-url`) ||
		!strings.Contains(secretsYAML, `name: litellm-secrets`) ||
		!strings.Contains(secretsYAML, `namespace: ai-platform`) {
		t.Fatalf("secrets.yaml missing metadata:\n%s", secretsYAML)
	}
	if strings.Contains(secretsYAML, "top-secret") || strings.Contains(secretsYAML, "postgres://secret") {
		t.Fatalf("secrets.yaml leaked plaintext:\n%s", secretsYAML)
	}

	sealedPath := "configs/orgs/myorg/projects/proj/services/svc/sealed-secrets/ai-platform/litellm-secrets.yaml"
	if got := string(repo.files[sealedPath]); got != "sealed-litellm-secrets" {
		t.Fatalf("sealed manifest: got %q", got)
	}

	d, err := s.GetConfig(ctx, "myorg", "proj", "svc")
	if err != nil {
		t.Fatalf("GetConfig after secret apply: %v", err)
	}
	if d.Secrets == nil || len(d.Secrets.Secrets) != 2 {
		t.Fatalf("expected two secret metadata entries, got %+v", d.Secrets)
	}

	logText := auditLogs.String()
	for _, want := range []string{"secret_admin_write", "success", "myorg", "proj", "svc", "ai-platform/litellm-secrets"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("audit log missing %q: %s", want, logText)
		}
	}
	for _, secretText := range []string{"top-secret", "postgres://secret"} {
		if strings.Contains(logText, secretText) {
			t.Fatalf("audit log leaked plaintext %q: %s", secretText, logText)
		}
	}
}

func TestStore_ApplyChanges_MergesSecretsFromCurrentRepo(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "svc")
	sealer := &fakeSealer{}
	applier := &fakeApplier{}
	s := store.New(repo, store.WithSecretDependencies(secret.Dependencies{
		Sealer:  sealer,
		Applier: applier,
	}))
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	// Simulate the local checkout being updated after the last in-memory
	// reload. Secret metadata must merge from the post-pull checkout used for
	// the commit rather than from the stale snapshot.
	seedSecretFiles(repo, "myorg", "proj", "svc")

	_, err := s.ApplyChanges(ctx, &store.ChangeRequest{
		Org:     "myorg",
		Project: "proj",
		Service: "svc",
		Secrets: map[string]store.SecretWrite{
			"litellm-secrets": {
				Namespace: "ai-platform",
				Data: map[string]secret.Value{
					"master-key": secret.NewValue([]byte("top-secret")),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyChanges secrets: %v", err)
	}

	secretsPath := "configs/orgs/myorg/projects/proj/services/svc/secrets.yaml"
	secretsYAML := string(repo.files[secretsPath])
	if !strings.Contains(secretsYAML, "existing-api-key") {
		t.Fatalf("existing repo secret metadata was lost:\n%s", secretsYAML)
	}
	if !strings.Contains(secretsYAML, "master-key") {
		t.Fatalf("new secret metadata was not written:\n%s", secretsYAML)
	}
}

func TestStore_ApplyChanges_SecretsRequireAdapters(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	req := &store.ChangeRequest{
		Org:     "myorg",
		Project: "proj",
		Service: "svc",
		Secrets: map[string]store.SecretWrite{
			"litellm-secrets": {
				Namespace: "ai-platform",
				Data: map[string]secret.Value{
					"master-key": secret.NewValue([]byte("top-secret")),
				},
			},
		},
	}
	_, err := s.ApplyChanges(ctx, req)
	if err == nil {
		t.Fatal("expected missing adapter validation error")
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.CodeValidation {
		t.Fatalf("expected CodeValidation, got %v", err)
	}
	if len(repo.files) != 0 {
		t.Fatalf("secret adapter validation should happen before commit, got files %v", repo.files)
	}
	if got := string(req.Secrets["litellm-secrets"].Data["master-key"].Bytes()); got != "" {
		t.Fatalf("secret plaintext should be destroyed after adapter validation failure, got %q", got)
	}
}

func TestStore_ApplyChanges_RejectsInvalidK8sSecretIdentity(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		secrets map[string]store.SecretWrite
	}{
		{
			name: "uppercase secret name",
			secrets: map[string]store.SecretWrite{
				"LiteLLM-Secrets": {
					Namespace: "ai-platform",
					Data:      map[string]secret.Value{"master-key": secret.NewValue([]byte("top-secret"))},
				},
			},
		},
		{
			name: "underscore namespace",
			secrets: map[string]store.SecretWrite{
				"litellm-secrets": {
					Namespace: "ai_platform",
					Data:      map[string]secret.Value{"master-key": secret.NewValue([]byte("top-secret"))},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			s := store.New(repo)
			if err := s.LoadFromRepo(ctx); err != nil {
				t.Fatalf("LoadFromRepo: %v", err)
			}

			_, err := s.ApplyChanges(ctx, &store.ChangeRequest{
				Org:     "myorg",
				Project: "proj",
				Service: "svc",
				Secrets: tc.secrets,
			})
			if err == nil {
				t.Fatal("expected invalid Kubernetes secret identity error")
			}
			var appErr *apperror.Error
			if !errors.As(err, &appErr) || appErr.Code != apperror.CodeValidation {
				t.Fatalf("expected CodeValidation, got %v", err)
			}
			if len(repo.files) != 0 {
				t.Fatalf("invalid Kubernetes secret identity should fail before commit, got files %v", repo.files)
			}
		})
	}
}

func TestStore_ApplyChanges_ReportsSecretApplyFailure(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	sealer := &fakeSealer{}
	applier := &fakeApplier{err: errors.New("apply boom")}
	s := store.New(repo, store.WithSecretDependencies(secret.Dependencies{
		Sealer:  sealer,
		Applier: applier,
	}))
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	result, err := s.ApplyChanges(ctx, &store.ChangeRequest{
		Org:     "myorg",
		Project: "proj",
		Service: "svc",
		Secrets: map[string]store.SecretWrite{
			"litellm-secrets": {
				Namespace: "ai-platform",
				Data: map[string]secret.Value{
					"master-key": secret.NewValue([]byte("top-secret")),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyChanges should not roll back committed secret on apply failure: %v", err)
	}
	if !result.ApplyFailed || !strings.Contains(result.ApplyError, "apply sealed secret ai-platform/litellm-secrets") {
		t.Fatalf("expected contextual apply failure, got %+v", result)
	}
}

func TestStore_ApplyChanges_AppliesSecretsAfterRequestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "svc")
	repo.afterCommit = cancel
	sealer := &fakeSealer{}
	applier := &fakeApplier{}
	s := store.New(repo, store.WithSecretDependencies(secret.Dependencies{
		Sealer:  sealer,
		Applier: applier,
	}))
	if err := s.LoadFromRepo(context.Background()); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	result, err := s.ApplyChanges(ctx, &store.ChangeRequest{
		Org:     "myorg",
		Project: "proj",
		Service: "svc",
		Secrets: map[string]store.SecretWrite{
			"litellm-secrets": {
				Namespace: "ai-platform",
				Data: map[string]secret.Value{
					"master-key": secret.NewValue([]byte("top-secret")),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyChanges: %v", err)
	}
	if result.ApplyFailed {
		t.Fatalf("apply should ignore request cancellation after commit: %+v", result)
	}
	if len(applier.manifests) != 1 {
		t.Fatalf("applier calls: got %d", len(applier.manifests))
	}
	if applier.ctxErr != nil {
		t.Fatalf("apply context should be detached from request cancellation, got %v", applier.ctxErr)
	}
}

func TestStore_ApplyChanges_Validation(t *testing.T) {
	ctx := context.Background()
	s := store.New(newFakeRepo())
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	tests := []struct {
		name string
		req  *store.ChangeRequest
	}{
		{"missing org", &store.ChangeRequest{Project: "p", Service: "s", Config: map[string]any{}}},
		{"missing project", &store.ChangeRequest{Org: "o", Service: "s", Config: map[string]any{}}},
		{"missing service", &store.ChangeRequest{Org: "o", Project: "p", Config: map[string]any{}}},
		{"no changes", &store.ChangeRequest{Org: "o", Project: "p", Service: "s"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.ApplyChanges(ctx, tc.req)
			if err == nil {
				t.Fatal("expected validation error")
			}
			var appErr *apperror.Error
			if !errors.As(err, &appErr) || appErr.Code != apperror.CodeValidation {
				t.Errorf("expected CodeValidation, got %v", err)
			}
		})
	}
}

func TestStore_DeleteChanges(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "svc")
	seedSecretFiles(repo, "myorg", "proj", "svc")

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	// Verify service exists first.
	_, err := s.GetConfig(ctx, "myorg", "proj", "svc")
	if err != nil {
		t.Fatalf("GetConfig before delete: %v", err)
	}

	result, err := s.DeleteChanges(ctx, &store.DeleteRequest{
		Org:     "myorg",
		Project: "proj",
		Service: "svc",
	})
	if err != nil {
		t.Fatalf("DeleteChanges: %v", err)
	}
	if result.Version == "" {
		t.Error("expected non-empty version")
	}
	if result.ReloadFailed {
		t.Errorf("ReloadFailed should be false on success, got error: %s", result.ReloadError)
	}
	if !strings.Contains(fmt.Sprint(result.DeletedFiles), "sealed-secrets/") {
		t.Fatalf("deleted files should include sealed-secret manifests, got %v", result.DeletedFiles)
	}

	// Service should be gone from memory (full reload sees deleted files).
	_, err = s.GetConfig(ctx, "myorg", "proj", "svc")
	if err == nil {
		t.Error("expected not-found after delete")
	}
	for path := range repo.files {
		if strings.Contains(path, "services/svc/") {
			t.Fatalf("service file remained after delete: %s", path)
		}
	}
}

// reloadFailingAfterDeleteRepo wraps fakeRepo so that Snapshot returns a
// broken YAML blob after DeleteAndPush, simulating a post-delete reload failure.
type reloadFailingAfterDeleteRepo struct {
	*fakeRepo
	failOnce bool
}

func (r *reloadFailingAfterDeleteRepo) DeleteAndPush(ctx context.Context, msg string, paths []string) (string, error) {
	hash, err := r.fakeRepo.DeleteAndPush(ctx, msg, paths)
	if err == nil {
		r.failOnce = true
	}
	return hash, err
}

func (r *reloadFailingAfterDeleteRepo) Snapshot(fn func(path string, data []byte) error) (string, error) {
	if r.failOnce {
		r.failOnce = false
		_ = fn("configs/orgs/o/projects/p/services/s/config.yaml", []byte(": broken"))
		hash, _ := r.HeadHash()
		return hash, nil
	}
	return r.fakeRepo.Snapshot(fn)
}

func TestStore_DeleteChanges_ReportsReloadFailure(t *testing.T) {
	ctx := context.Background()
	inner := newFakeRepo()
	seedFakeRepo(inner, "myorg", "proj", "svc")
	repo := &reloadFailingAfterDeleteRepo{fakeRepo: inner}

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	goodVersion := s.HeadVersion()

	res, err := s.DeleteChanges(ctx, &store.DeleteRequest{
		Org: "myorg", Project: "proj", Service: "svc",
	})
	if err != nil {
		t.Fatalf("DeleteChanges must succeed even if post-delete reload fails: %v", err)
	}
	if !res.ReloadFailed {
		t.Error("ReloadFailed should be true")
	}
	if res.ReloadError == "" {
		t.Error("ReloadError should be populated")
	}

	// Last-known-good snapshot must still be in place.
	if v := s.HeadVersion(); v != goodVersion {
		t.Errorf("HeadVersion changed after failed reload: %q → %q", goodVersion, v)
	}
	if _, err := s.GetConfig(ctx, "myorg", "proj", "svc"); err != nil {
		t.Errorf("last-known-good snapshot lost after failed delete reload: %v", err)
	}
	if !s.IsDegraded() {
		t.Error("store should be degraded after reload failure")
	}
}

func TestStore_EnvVarsOnlyService_PropagatesMetadataUpdatedAt(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	base := "configs/orgs/myorg/projects/proj/services/envonly"
	want := "2026-01-15T09:30:00Z"
	repo.files[base+"/env_vars.yaml"] = []byte(`version: "1"
metadata:
  service: envonly
  org: myorg
  project: proj
  updated_at: "` + want + `"
env_vars:
  plain:
    LOG_LEVEL: "DEBUG"
`)

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	svcs := s.ListServices("myorg", "proj")
	if len(svcs) != 1 {
		t.Fatalf("want 1 service, got %d", len(svcs))
	}
	wantT, _ := time.Parse(time.RFC3339, want)
	if !svcs[0].UpdatedAt.Equal(wantT) {
		t.Errorf("UpdatedAt for env-only service: want %v, got %v", wantT, svcs[0].UpdatedAt)
	}
}

func TestStore_UpdatedAt_PicksMaxAcrossFiles(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	base := "configs/orgs/o/projects/p/services/s"
	olderCfg := "2026-02-01T00:00:00Z"
	newerEnv := "2026-03-20T00:00:00Z"

	repo.files[base+"/config.yaml"] = []byte(`version: "1"
metadata:
  service: s
  org: o
  project: p
  updated_at: "` + olderCfg + `"
config:
  k: v
`)
	repo.files[base+"/env_vars.yaml"] = []byte(`version: "1"
metadata:
  service: s
  org: o
  project: p
  updated_at: "` + newerEnv + `"
env_vars:
  plain:
    FOO: "bar"
`)

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	svcs := s.ListServices("o", "p")
	if len(svcs) != 1 {
		t.Fatalf("want 1 service, got %d", len(svcs))
	}
	wantT, _ := time.Parse(time.RFC3339, newerEnv)
	if !svcs[0].UpdatedAt.Equal(wantT) {
		t.Errorf("expected newer env_vars timestamp %v, got %v", wantT, svcs[0].UpdatedAt)
	}
}

// TestStore_LoadFromRepo_PullsBeforeFirstReload guards the P2 review item:
// a stale local clone (dev box, persistent volume) must not serve Phase-1
// traffic until the first background poll tick catches up. LoadFromRepo
// must pull once before building the first snapshot.
func TestStore_LoadFromRepo_PullsBeforeFirstReload(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}
	if repo.pullCalls != 1 {
		t.Errorf("expected LoadFromRepo to call Pull exactly once, got %d", repo.pullCalls)
	}
}

// pullErrRepo is a fakeRepo whose Pull returns a configured error. Used to
// exercise the LoadFromRepo error-propagation policy.
type pullErrRepo struct {
	*fakeRepo
	pullErr error
}

func (r *pullErrRepo) Pull(ctx context.Context) (string, bool, error) {
	r.mu.Lock()
	r.pullCalls++
	r.mu.Unlock()
	return "", false, r.pullErr
}

// TestStore_LoadFromRepo_PropagatesContextCancellation ensures startup honors
// a canceled / deadline-exceeded context: a pull failure tied to context is
// fatal so callers can actually abort startup, while transient non-context
// pull errors fall back to the on-disk checkout (covered separately below).
func TestStore_LoadFromRepo_PropagatesContextCancellation(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"canceled", context.Canceled},
		{"deadline exceeded", context.DeadlineExceeded},
		{"wrapped canceled", fmt.Errorf("pull: %w", context.Canceled)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &pullErrRepo{fakeRepo: newFakeRepo(), pullErr: tc.err}
			s := store.New(repo)
			err := s.LoadFromRepo(context.Background())
			if err == nil {
				t.Fatal("expected LoadFromRepo to fail on context-cancellation pull error")
			}
			if !errors.Is(err, tc.err) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("error chain should preserve context error, got %v", err)
			}
		})
	}
}

// TestStore_LoadFromRepo_TolerantToTransientPullFailure asserts the other
// half of the policy: a non-context pull error (e.g. transient network blip)
// is logged and startup continues using the on-disk checkout.
func TestStore_LoadFromRepo_TolerantToTransientPullFailure(t *testing.T) {
	repo := &pullErrRepo{fakeRepo: newFakeRepo(), pullErr: errors.New("boom: network unreachable")}
	s := store.New(repo)
	if err := s.LoadFromRepo(context.Background()); err != nil {
		t.Fatalf("LoadFromRepo should tolerate transient pull failure, got %v", err)
	}
}

func TestStore_HeadVersion(t *testing.T) {
	ctx := context.Background()
	s := store.New(newFakeRepo())
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}
	if v := s.HeadVersion(); v != "abc123" {
		t.Errorf("HeadVersion: want abc123, got %q", v)
	}
}

func TestStore_ResourceVersionTracksResourceContent(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	configVersion, headVersion, err := s.ResourceVersion(ctx, "myorg", "proj", "litellm", "config")
	if err != nil {
		t.Fatalf("ResourceVersion config: %v", err)
	}
	envVersion, _, err := s.ResourceVersion(ctx, "myorg", "proj", "litellm", "env_vars")
	if err != nil {
		t.Fatalf("ResourceVersion env_vars: %v", err)
	}
	if configVersion != "abc123" || envVersion != "abc123" || headVersion != "abc123" {
		t.Fatalf("initial versions: config=%q env=%q head=%q", configVersion, envVersion, headVersion)
	}

	base := "configs/orgs/myorg/projects/proj/services/litellm"
	repo.mu.Lock()
	repo.files[base+"/config.yaml"] = []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
config:
  router_settings:
    num_retries: 4
`)
	repo.commitHash = "def456"
	repo.nextPullUpdated = true
	repo.mu.Unlock()
	if _, err := s.RefreshFromRepo(ctx); err != nil {
		t.Fatalf("RefreshFromRepo config change: %v", err)
	}

	configVersion, headVersion, err = s.ResourceVersion(ctx, "myorg", "proj", "litellm", "config")
	if err != nil {
		t.Fatalf("ResourceVersion config after config change: %v", err)
	}
	envVersion, _, err = s.ResourceVersion(ctx, "myorg", "proj", "litellm", "env_vars")
	if err != nil {
		t.Fatalf("ResourceVersion env_vars after config change: %v", err)
	}
	if configVersion != "def456" || envVersion != "abc123" || headVersion != "def456" {
		t.Fatalf("after config-only change: config=%q env=%q head=%q", configVersion, envVersion, headVersion)
	}

	repo.mu.Lock()
	repo.files[base+"/env_vars.yaml"] = []byte(`version: "1"
metadata:
  service: litellm
  org: myorg
  project: proj
env_vars:
  plain:
    LOG_LEVEL: "DEBUG"
  secret_refs:
    API_KEY: "my-api-key"
`)
	repo.commitHash = "fed789"
	repo.nextPullUpdated = true
	repo.mu.Unlock()
	if _, err := s.RefreshFromRepo(ctx); err != nil {
		t.Fatalf("RefreshFromRepo env change: %v", err)
	}

	envVersion, headVersion, err = s.ResourceVersion(ctx, "myorg", "proj", "litellm", "env_vars")
	if err != nil {
		t.Fatalf("ResourceVersion env_vars after env change: %v", err)
	}
	if envVersion != "fed789" || headVersion != "fed789" {
		t.Fatalf("after env change: env=%q head=%q", envVersion, headVersion)
	}
}

func TestStore_WaitForVersionChangeReturnsImmediatelyWhenVersionDiffers(t *testing.T) {
	ctx := context.Background()
	s := store.New(newFakeRepo())
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	version, changed, err := s.WaitForVersionChange(ctx, "stale-version")
	if err != nil {
		t.Fatalf("WaitForVersionChange: %v", err)
	}
	if !changed || version != "abc123" {
		t.Fatalf("WaitForVersionChange: version=%q changed=%v", version, changed)
	}
}

func TestStore_WaitForVersionChangeNotifiesAfterSuccessfulRefresh(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	waitCh := waitForVersionChangeAsync(ctx, s, "abc123")

	repo.mu.Lock()
	repo.commitHash = "def456"
	repo.nextPullUpdated = true
	repo.mu.Unlock()
	updated, err := s.RefreshFromRepo(ctx)
	if err != nil {
		t.Fatalf("RefreshFromRepo: %v", err)
	}
	if !updated {
		t.Fatal("expected refresh update")
	}

	result := receiveWaitResult(t, waitCh)
	if result.err != nil {
		t.Fatalf("WaitForVersionChange: %v", result.err)
	}
	if !result.changed || result.version != "def456" {
		t.Fatalf("WaitForVersionChange result: %+v", result)
	}
}

func TestStore_WaitForVersionChangeDoesNotNotifyOnFailedRefresh(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	waitCh := waitForVersionChangeAsync(waitCtx, s, "abc123")

	repo.mu.Lock()
	repo.files["configs/orgs/myorg/projects/proj/services/litellm/config.yaml"] = []byte("::: not valid yaml :::")
	repo.commitHash = "bad456"
	repo.nextPullUpdated = true
	repo.mu.Unlock()
	updated, err := s.RefreshFromRepo(ctx)
	if err == nil {
		t.Fatal("expected refresh error on malformed YAML")
	}
	if updated {
		t.Fatal("failed refresh should not report updated")
	}

	result := receiveWaitResult(t, waitCh)
	if !errors.Is(result.err, context.DeadlineExceeded) {
		t.Fatalf("WaitForVersionChange error: got %v, want context deadline", result.err)
	}
	if result.changed || result.version != "abc123" {
		t.Fatalf("failed refresh should not notify version change: %+v", result)
	}
}

func TestStore_WaitForVersionChangeContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := newFakeRepo()
	s := store.New(repo)
	if err := s.LoadFromRepo(context.Background()); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}
	cancel()

	version, changed, err := s.WaitForVersionChange(ctx, "abc123")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForVersionChange error: got %v, want context.Canceled", err)
	}
	if changed || version != "abc123" {
		t.Fatalf("WaitForVersionChange result after cancel: version=%q changed=%v", version, changed)
	}
}

func TestStore_RefreshFromRepo_NoChange(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	updated, err := s.RefreshFromRepo(ctx)
	if err != nil {
		t.Fatalf("RefreshFromRepo: %v", err)
	}
	if updated {
		t.Error("expected no update since HEAD didn't move")
	}
}

func TestStore_RecordsReloadMetrics(t *testing.T) {
	metrics.ResetForTest()
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")
	s := store.New(repo)

	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}
	if _, err := s.RefreshFromRepo(ctx); err != nil {
		t.Fatalf("RefreshFromRepo: %v", err)
	}
	repo.mu.Lock()
	repo.files["configs/orgs/myorg/projects/proj/services/litellm/config.yaml"] = []byte("::: not valid yaml :::")
	repo.commitHash = "bad456"
	repo.nextPullUpdated = true
	repo.mu.Unlock()
	if _, err := s.RefreshFromRepo(ctx); err == nil {
		t.Fatal("expected refresh error")
	}

	body := string(metrics.RenderPrometheus(nil))
	checks := []string{
		`aap_config_server_reload_attempts_total{mode="initial",outcome="loaded"} 1`,
		`aap_config_server_reload_attempts_total{mode="background",outcome="unchanged"} 1`,
		`aap_config_server_reload_attempts_total{mode="background",outcome="error"} 1`,
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Fatalf("metrics body missing %q:\n%s", check, body)
		}
	}
}

// TestStore_ReloadFromRepo_ForcesReloadWhenHeadUnchanged guards the P1 admin-
// reload semantics: force reload must re-parse the current checkout even when
// the remote has not moved, so a degraded store recovers after the operator
// fixes the offending YAML and hits POST /api/v1/admin/reload.
func TestStore_ReloadFromRepo_ForcesReloadWhenHeadUnchanged(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	// Poison the checkout without advancing HEAD, then try to recover via
	// a background refresh. RefreshFromRepo must short-circuit (HEAD didn't
	// move), leaving the store healthy.
	repo.mu.Lock()
	repo.files["configs/orgs/myorg/projects/proj/services/litellm/config.yaml"] = []byte("::: not yaml :::")
	repo.mu.Unlock()

	if _, err := s.RefreshFromRepo(ctx); err != nil {
		t.Fatalf("RefreshFromRepo should be a no-op when HEAD doesn't move: %v", err)
	}
	if s.IsDegraded() {
		t.Fatal("RefreshFromRepo must not degrade the store when HEAD didn't move")
	}

	// An operator force reload, however, must re-parse and surface the parse
	// failure — this is the bug the P1 review called out.
	if _, err := s.ReloadFromRepo(ctx); err == nil {
		t.Fatal("ReloadFromRepo must fail when current checkout has malformed YAML")
	}
	if !s.IsDegraded() {
		t.Error("store should be degraded after force reload hits malformed YAML")
	}

	// Now fix the file in place (still no HEAD move) and force reload again;
	// the store should recover because ReloadFromRepo re-parses unconditionally.
	seedFakeRepo(repo, "myorg", "proj", "litellm") // restore good yaml

	updated, err := s.ReloadFromRepo(ctx)
	if err != nil {
		t.Fatalf("ReloadFromRepo recovery: %v", err)
	}
	if !updated {
		t.Error("updated should be true when recovering from a degraded state")
	}
	if s.IsDegraded() {
		t.Error("store should no longer be degraded after successful force reload")
	}
}

func TestStore_ApplyChanges_PathTraversal(t *testing.T) {
	ctx := context.Background()
	s := store.New(newFakeRepo())
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	badNames := []string{"../etc", "a/b", "my org", "..", "a b", ""}
	for _, bad := range badNames {
		req := &store.ChangeRequest{
			Org:     bad,
			Project: "proj",
			Service: "svc",
			Config:  map[string]any{},
		}
		if req.Org == "" {
			req.Org = "org"
			req.Project = bad
		}
		_, err := s.ApplyChanges(ctx, req)
		if err == nil {
			t.Errorf("ApplyChanges with %q: expected error, got nil", bad)
			continue
		}
		var appErr *apperror.Error
		if !errors.As(err, &appErr) {
			t.Errorf("ApplyChanges with %q: expected apperror, got %T", bad, err)
		}
	}
}

func TestStore_Reload_FailClosedOnMalformedYAML(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "myorg", "proj", "litellm")

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	// Confirm the last-known-good snapshot is present.
	if _, err := s.GetConfig(ctx, "myorg", "proj", "litellm"); err != nil {
		t.Fatalf("initial GetConfig: %v", err)
	}
	goodVersion := s.HeadVersion()

	// Introduce a malformed file and move HEAD forward.
	repo.files["configs/orgs/myorg/projects/proj/services/litellm/config.yaml"] = []byte("::: not valid yaml :::")
	repo.commitHash = "newhead"
	repo.nextPullUpdated = true

	updated, err := s.RefreshFromRepo(ctx)
	if err == nil {
		t.Fatal("expected refresh error on malformed YAML")
	}
	if updated {
		t.Error("updated should be false when reload failed")
	}

	// Snapshot must NOT have been replaced.
	if v := s.HeadVersion(); v != goodVersion {
		t.Errorf("HeadVersion changed after failed reload: %q → %q", goodVersion, v)
	}
	if _, err := s.GetConfig(ctx, "myorg", "proj", "litellm"); err != nil {
		t.Errorf("last-known-good snapshot lost after failed reload: %v", err)
	}
	if !s.IsDegraded() {
		t.Error("store should be degraded after failed reload")
	}
}

// reloadFailingRepo mimics a repo whose Snapshot returns a fresh HEAD hash but
// a broken yaml blob, so ApplyChanges can commit successfully and then observe
// a post-commit reload failure.
type reloadFailingRepo struct {
	*fakeRepo
	fail bool
}

func (r *reloadFailingRepo) Snapshot(fn func(path string, data []byte) error) (string, error) {
	if r.fail {
		// Feed one malformed file so parse fails inside reload.
		_ = fn("configs/orgs/o/projects/p/services/s/config.yaml", []byte(": broken"))
		hash, _ := r.HeadHash()
		return hash, nil
	}
	return r.fakeRepo.Snapshot(fn)
}

func TestStore_ApplyChanges_ReportsReloadFailure(t *testing.T) {
	ctx := context.Background()
	inner := newFakeRepo()
	repo := &reloadFailingRepo{fakeRepo: inner}
	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	// Now make reload fail for the next call.
	repo.fail = true

	res, err := s.ApplyChanges(ctx, &store.ChangeRequest{
		Org: "o", Project: "p", Service: "s",
		Config: map[string]any{"k": "v"},
	})
	if err != nil {
		t.Fatalf("ApplyChanges must succeed even if post-commit reload fails, got %v", err)
	}
	if !res.ReloadFailed {
		t.Error("ReloadFailed should be true")
	}
	if res.ReloadError == "" {
		t.Error("ReloadError should be populated")
	}
}

func TestStore_Concurrent_Reads(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "org", "p", "svc")

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				_, _ = s.GetConfig(ctx, "org", "p", "svc")
				time.Sleep(time.Millisecond)
			}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestStore_Concurrent_RefreshApplyRead hammers the store with concurrent
// refreshes, admin writes, and reads. The race detector should be clean and
// GetConfig must never observe a partial snapshot (either the service is
// absent, or it has a non-nil Config — never a half-filled ServiceData).
func TestStore_Concurrent_RefreshApplyRead(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	seedFakeRepo(repo, "org", "p", "svc")

	s := store.New(repo)
	if err := s.LoadFromRepo(ctx); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}

	const (
		readers   = 8
		refreshes = 4
		writers   = 2
		iters     = 30
	)

	var wg sync.WaitGroup

	// Readers: any service we find must have its Config populated (since every
	// seeded/committed file carries config).
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				d, err := s.GetConfig(ctx, "org", "p", "svc")
				if err == nil && d.Config == nil && d.EnvVars == nil && d.Secrets == nil {
					t.Errorf("observed empty ServiceData for org/p/svc (partial snapshot)")
					return
				}
			}
		}()
	}

	// Refreshers: alternate between "no change" and "HEAD moved".
	for i := 0; i < refreshes; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				if j%2 == 0 {
					repo.mu.Lock()
					repo.nextPullUpdated = true
					repo.mu.Unlock()
				}
				if _, err := s.RefreshFromRepo(ctx); err != nil {
					t.Errorf("refresher %d: RefreshFromRepo: %v", id, err)
					return
				}
			}
		}(i)
	}

	// Writers: run ApplyChanges for different services.
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			svcName := "writer-svc"
			// Keep names valid for validateName.
			if id == 1 {
				svcName = "writer.svc-1"
			}
			for j := 0; j < iters; j++ {
				_, err := s.ApplyChanges(ctx, &store.ChangeRequest{
					Org: "org", Project: "p", Service: svcName,
					Config: map[string]any{"n": j},
				})
				if err != nil {
					t.Errorf("writer %d: ApplyChanges: %v", id, err)
					return
				}
			}
		}(i)
	}

	wg.Wait()
}

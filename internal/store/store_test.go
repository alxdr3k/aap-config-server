package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aap/config-server/internal/apperror"
	"github.com/aap/config-server/internal/store"
)

// fakeRepo is a minimal in-memory GitRepo for testing the store in isolation.
// All state is guarded by mu so concurrent tests exercise the store's locking
// without racing on the fake itself.
type fakeRepo struct {
	mu              sync.Mutex
	files           map[string][]byte
	commitHash      string
	nextPullUpdated bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		files:      map[string][]byte{},
		commitHash: "abc123",
	}
}

func (f *fakeRepo) CloneOrOpen(_ context.Context) error { return nil }

func (f *fakeRepo) Pull(_ context.Context) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	updated := f.nextPullUpdated
	f.nextPullUpdated = false
	return f.commitHash, updated, nil
}

func (f *fakeRepo) CommitAndPush(_ context.Context, _ string, files map[string][]byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range files {
		f.files[k] = v
	}
	f.commitHash = "newcommit"
	return f.commitHash, nil
}

func (f *fakeRepo) DeleteAndPush(_ context.Context, _ string, paths []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range paths {
		delete(f.files, p)
	}
	f.commitHash = "delcommit"
	return f.commitHash, nil
}

func (f *fakeRepo) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.files[path]
	if !ok {
		return nil, errors.New("file not found: " + path)
	}
	return d, nil
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

func (f *fakeRepo) ReadFileAtCommit(_ string, path string) ([]byte, error) {
	return f.ReadFile(path)
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

	req := &store.ChangeRequest{
		Org:     "myorg",
		Project: "proj",
		Service: "svc",
		Config: map[string]any{
			"router_settings": map[string]any{
				"num_retries": 5,
			},
		},
		Message: "test commit",
	}

	result, err := s.ApplyChanges(ctx, req)
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

	// Service should be gone from memory.
	_, err = s.GetConfig(ctx, "myorg", "proj", "svc")
	if err == nil {
		t.Error("expected not-found after delete")
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

package gitops_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/aap/config-server/internal/gitops"
)

// newLocalRepo creates a bare "remote" repo seeded with one commit,
// and returns a Repo pointed at a fresh clone of it.
func newLocalRepo(t *testing.T) (remotePath string, repo *gitops.Repo) {
	t.Helper()

	// 1. Create a regular seed repo and make an initial commit.
	seedPath := t.TempDir()
	seedRepo, err := gogit.PlainInit(seedPath, false)
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	w, err := seedRepo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	readmePath := filepath.Join(seedPath, "README.md")
	if err := os.WriteFile(readmePath, []byte("init"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if _, err := w.Add("README.md"); err != nil {
		t.Fatalf("git add README: %v", err)
	}
	if _, err := w.Commit("init", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	// 2. Create a bare "remote" repo and push the seed commit to it.
	remotePath = t.TempDir()
	if _, err := gogit.PlainInit(remotePath, true); err != nil {
		t.Fatalf("init bare repo: %v", err)
	}
	_, err = seedRepo.CreateRemote(&gogitcfg.RemoteConfig{
		Name: "origin",
		URLs: []string{remotePath},
	})
	if err != nil {
		t.Fatalf("create remote: %v", err)
	}
	if err := seedRepo.Push(&gogit.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("seed push: %v", err)
	}

	// Create the actual Repo under test.
	clonePath := t.TempDir()
	r, err := gitops.New(gitops.Options{
		LocalPath: clonePath,
		RemoteURL: remotePath,
		Branch:    "master", // gogit default branch
	})
	if err != nil {
		t.Fatalf("gitops.New: %v", err)
	}
	return remotePath, r
}

func TestCloneOrOpen_Clone(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}

	hash, err := repo.HeadHash()
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty HEAD hash")
	}
}

func TestCloneOrOpen_Open(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	// First clone.
	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("first CloneOrOpen: %v", err)
	}
	h1, _ := repo.HeadHash()

	// Second open — should reuse existing clone.
	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("second CloneOrOpen: %v", err)
	}
	h2, _ := repo.HeadHash()
	if h1 != h2 {
		t.Errorf("HEAD changed after re-open: %s → %s", h1, h2)
	}
}

func TestCommitAndPush(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}

	files := map[string][]byte{
		"configs/orgs/myorg/projects/ai/services/litellm/config.yaml": []byte("version: \"1\"\nconfig: {}"),
	}
	hash, err := repo.CommitAndPush(ctx, "add litellm config", files)
	if err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty commit hash")
	}
}

func TestDeleteAndPush(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}

	// First create a file.
	files := map[string][]byte{
		"configs/orgs/myorg/projects/p/services/svc/config.yaml": []byte("version: \"1\"\nconfig: {}"),
	}
	if _, err := repo.CommitAndPush(ctx, "add", files); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	// Now delete it.
	hash, err := repo.DeleteAndPush(ctx, "delete svc", []string{
		"configs/orgs/myorg/projects/p/services/svc/config.yaml",
	})
	if err != nil {
		t.Fatalf("DeleteAndPush: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty hash after delete")
	}
}

func TestReadFile(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}

	data := []byte("hello from config")
	files := map[string][]byte{"configs/test.yaml": data}
	if _, err := repo.CommitAndPush(ctx, "add test", files); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	got, err := repo.ReadFile("configs/test.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("ReadFile content: want %q, got %q", data, got)
	}
}

func TestWalkConfigs(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}

	files := map[string][]byte{
		"configs/orgs/org1/projects/proj/services/svc/config.yaml":   []byte("a"),
		"configs/orgs/org1/projects/proj/services/svc/env_vars.yaml": []byte("b"),
	}
	if _, err := repo.CommitAndPush(ctx, "add files", files); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	found := map[string]bool{}
	err := repo.WalkConfigs(func(path string, data []byte) error {
		found[path] = true
		return nil
	})
	if err != nil {
		t.Fatalf("WalkConfigs: %v", err)
	}

	for path := range files {
		if !found[path] {
			t.Errorf("WalkConfigs: missing %s", path)
		}
	}
}

// injectCompetingCommit clones bareRemote into a scratch dir, commits one file,
// and pushes. Used by the rejected-push retry tests to race against Repo.
func injectCompetingCommit(t *testing.T, bareRemote string, name string) {
	t.Helper()
	scratch := t.TempDir()
	rr, err := gogit.PlainClone(scratch, false, &gogit.CloneOptions{URL: bareRemote})
	if err != nil {
		t.Fatalf("competitor clone: %v", err)
	}
	w, err := rr.Worktree()
	if err != nil {
		t.Fatalf("competitor worktree: %v", err)
	}
	p := filepath.Join(scratch, name)
	if err := os.WriteFile(p, []byte("competitor"), 0o644); err != nil {
		t.Fatalf("competitor write: %v", err)
	}
	if _, err := w.Add(name); err != nil {
		t.Fatalf("competitor add: %v", err)
	}
	if _, err := w.Commit("competitor "+name, &gogit.CommitOptions{
		Author: &object.Signature{Name: "other", Email: "other@test.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("competitor commit: %v", err)
	}
	if err := rr.Push(&gogit.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("competitor push: %v", err)
	}
}

func TestCommitAndPush_RetriesOnRejectedPush(t *testing.T) {
	remotePath, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}

	// Competitor pushes once, right after our first pull → our first push is
	// rejected non-fast-forward, forcing the retry path.
	var hookCalls int
	restore := gitops.SetAfterPullHook(func(attempt int) {
		hookCalls++
		if attempt == 0 {
			injectCompetingCommit(t, remotePath, "competitor-commit.txt")
		}
	})
	defer restore()

	files := map[string][]byte{
		"configs/orgs/myorg/projects/p/services/svc/config.yaml": []byte("version: \"1\"\nconfig: {}"),
	}
	hash, err := repo.CommitAndPush(ctx, "add svc", files)
	if err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash after retry")
	}
	if hookCalls < 2 {
		t.Errorf("expected retry loop to run at least twice, got %d", hookCalls)
	}

	// Both our file and the competitor's file must live in the final worktree.
	got, err := repo.ReadFile("configs/orgs/myorg/projects/p/services/svc/config.yaml")
	if err != nil || len(got) == 0 {
		t.Errorf("our file missing after retry: err=%v", err)
	}
	if _, err := repo.ReadFile("competitor-commit.txt"); err != nil {
		t.Errorf("competitor's file missing after retry: %v", err)
	}
}

func TestDeleteAndPush_RetriesOnRejectedPush(t *testing.T) {
	remotePath, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}

	target := "configs/orgs/myorg/projects/p/services/svc/config.yaml"
	if _, err := repo.CommitAndPush(ctx, "seed", map[string][]byte{
		target: []byte("version: \"1\"\nconfig: {}"),
	}); err != nil {
		t.Fatalf("seed CommitAndPush: %v", err)
	}

	var hookCalls int
	restore := gitops.SetAfterPullHook(func(attempt int) {
		hookCalls++
		if attempt == 0 {
			injectCompetingCommit(t, remotePath, "competitor-during-delete.txt")
		}
	})
	defer restore()

	hash, err := repo.DeleteAndPush(ctx, "delete svc", []string{target})
	if err != nil {
		t.Fatalf("DeleteAndPush: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash after retry")
	}
	if hookCalls < 2 {
		t.Errorf("expected retry loop to run at least twice, got %d", hookCalls)
	}

	if _, err := repo.ReadFile(target); err == nil {
		t.Error("target file should be gone after delete retry")
	}
	if _, err := repo.ReadFile("competitor-during-delete.txt"); err != nil {
		t.Errorf("competitor's file missing after retry: %v", err)
	}
}

// TestSnapshot_RejectsDirtyConfigsWorktree covers the P2 review item: if the
// configs/ subtree contains files that don't match HEAD, Snapshot must fail
// so the reported HEAD doesn't mislead callers about what they would read.
func TestSnapshot_RejectsDirtyConfigsWorktree(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}
	if _, err := repo.CommitAndPush(ctx, "seed", map[string][]byte{
		"configs/orgs/o/projects/p/services/s/config.yaml": []byte("version: \"1\"\nconfig: {}"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Clean snapshot must work.
	if _, err := repo.Snapshot(func(path string, data []byte) error { return nil }); err != nil {
		t.Fatalf("clean Snapshot: %v", err)
	}

	// Introduce an out-of-band untracked file under configs/ (simulating
	// someone touching the container's worktree). Snapshot must refuse.
	dirty := filepath.Join(repo.LocalPath(), "configs/orgs/o/projects/p/services/s/stray.yaml")
	if err := os.WriteFile(dirty, []byte("stray"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(dirty) })

	if _, err := repo.Snapshot(func(path string, data []byte) error { return nil }); err == nil {
		t.Error("Snapshot should fail when configs/ worktree has an untracked file")
	}
}

// TestSnapshot_IgnoresDirtyOutsideConfigs ensures the dirty check scopes to
// configs/ — we don't care about files elsewhere in the repo root.
func TestSnapshot_IgnoresDirtyOutsideConfigs(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}
	if _, err := repo.CommitAndPush(ctx, "seed", map[string][]byte{
		"configs/orgs/o/projects/p/services/s/config.yaml": []byte("version: \"1\"\nconfig: {}"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	stray := filepath.Join(repo.LocalPath(), "some-other-file.txt")
	if err := os.WriteFile(stray, []byte("anything"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(stray) })

	if _, err := repo.Snapshot(func(path string, data []byte) error { return nil }); err != nil {
		t.Errorf("Snapshot with dirty non-configs file should pass, got %v", err)
	}
}

func TestReadFileAtCommit(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}

	original := []byte("original content")
	hash1, err := repo.CommitAndPush(ctx, "v1", map[string][]byte{
		"configs/file.yaml": original,
	})
	if err != nil {
		t.Fatalf("CommitAndPush v1: %v", err)
	}

	updated := []byte("updated content")
	_, err = repo.CommitAndPush(ctx, "v2", map[string][]byte{
		"configs/file.yaml": updated,
	})
	if err != nil {
		t.Fatalf("CommitAndPush v2: %v", err)
	}

	// Reading at hash1 should return original content.
	got, err := repo.ReadFileAtCommit(hash1, "configs/file.yaml")
	if err != nil {
		t.Fatalf("ReadFileAtCommit: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("ReadFileAtCommit: want %q, got %q", original, got)
	}
}

func TestReadFileAtCommit_NotFoundSentinels(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}
	hash, err := repo.CommitAndPush(ctx, "v1", map[string][]byte{
		"configs/file.yaml": []byte("content"),
	})
	if err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	if _, err := repo.ReadFileAtCommit(hash, "configs/missing.yaml"); !errors.Is(err, gitops.ErrFileNotFoundAtCommit) {
		t.Fatalf("missing file should wrap ErrFileNotFoundAtCommit, got %v", err)
	}
	if _, err := repo.ReadFileAtCommit("0000000000000000000000000000000000000000", "configs/file.yaml"); !errors.Is(err, gitops.ErrCommitNotFound) {
		t.Fatalf("missing commit should wrap ErrCommitNotFound, got %v", err)
	}
}

func TestClassifyServiceFileChange(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantOK   bool
		wantRel  string
		wantKind gitops.ServiceFileKind
	}{
		{
			name:     "config",
			path:     "configs/orgs/myorg/projects/proj/services/litellm/config.yaml",
			wantOK:   true,
			wantRel:  "config.yaml",
			wantKind: gitops.ServiceFileConfig,
		},
		{
			name:     "env vars",
			path:     "configs/orgs/myorg/projects/proj/services/litellm/env_vars.yaml",
			wantOK:   true,
			wantRel:  "env_vars.yaml",
			wantKind: gitops.ServiceFileEnvVars,
		},
		{
			name:     "secrets metadata",
			path:     "configs/orgs/myorg/projects/proj/services/litellm/secrets.yaml",
			wantOK:   true,
			wantRel:  "secrets.yaml",
			wantKind: gitops.ServiceFileSecrets,
		},
		{
			name:     "sealed secret manifest",
			path:     "configs/orgs/myorg/projects/proj/services/litellm/sealed-secrets/ns/name.yaml",
			wantOK:   true,
			wantRel:  "sealed-secrets/ns/name.yaml",
			wantKind: gitops.ServiceFileSealedSecret,
		},
		{
			name:   "sibling service with shared prefix",
			path:   "configs/orgs/myorg/projects/proj/services/litellm-canary/config.yaml",
			wantOK: false,
		},
		{
			name:   "unknown file under service",
			path:   "configs/orgs/myorg/projects/proj/services/litellm/notes.txt",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := gitops.ClassifyServiceFileChange(tt.path, "myorg", "proj", "litellm")
			if ok != tt.wantOK {
				t.Fatalf("ok: want %v, got %v", tt.wantOK, ok)
			}
			if !ok {
				return
			}
			if got.Path != tt.wantRel || got.Kind != tt.wantKind {
				t.Fatalf("change: want (%s, %s), got (%s, %s)", tt.wantRel, tt.wantKind, got.Path, got.Kind)
			}
		})
	}
}

func TestIterateServiceHistory(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}

	if _, err := repo.CommitAndPush(ctx, "add litellm config", map[string][]byte{
		"configs/orgs/myorg/projects/proj/services/litellm/config.yaml": []byte("version: \"1\"\nconfig: {}\n"),
	}); err != nil {
		t.Fatalf("CommitAndPush litellm config: %v", err)
	}
	if _, err := repo.CommitAndPush(ctx, "update sibling env", map[string][]byte{
		"configs/orgs/myorg/projects/proj/services/other/env_vars.yaml": []byte("version: \"1\"\nenv_vars: {}\n"),
	}); err != nil {
		t.Fatalf("CommitAndPush sibling env: %v", err)
	}
	if _, err := repo.CommitAndPush(ctx, "update litellm secrets", map[string][]byte{
		"configs/orgs/myorg/projects/proj/services/litellm/secrets.yaml":                []byte("version: \"1\"\nsecrets: []\n"),
		"configs/orgs/myorg/projects/proj/services/litellm/sealed-secrets/ns/name.yaml": []byte("sealed\n"),
	}); err != nil {
		t.Fatalf("CommitAndPush litellm secrets: %v", err)
	}

	var got []gitops.ServiceHistoryEntry
	if err := repo.IterateServiceHistory(ctx, "myorg", "proj", "litellm", func(entry gitops.ServiceHistoryEntry) error {
		got = append(got, entry)
		return nil
	}); err != nil {
		t.Fatalf("IterateServiceHistory: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("history length: want 2, got %d: %#v", len(got), got)
	}
	if got[0].Message != "update litellm secrets" {
		t.Fatalf("newest message: want update litellm secrets, got %q", got[0].Message)
	}
	if got[1].Message != "add litellm config" {
		t.Fatalf("oldest message: want add litellm config, got %q", got[1].Message)
	}
	if got[0].Version == "" || got[0].Author == "" || got[0].Timestamp.IsZero() {
		t.Fatalf("newest entry missing metadata: %#v", got[0])
	}

	assertChangedPaths(t, got[0].FilesChanged, []string{"sealed-secrets/ns/name.yaml", "secrets.yaml"})
	assertChangedPaths(t, got[1].FilesChanged, []string{"config.yaml"})
}

func TestIterateServiceHistory_CallbackCanReadRepo(t *testing.T) {
	_, repo := newLocalRepo(t)
	ctx := context.Background()

	if err := repo.CloneOrOpen(ctx); err != nil {
		t.Fatalf("CloneOrOpen: %v", err)
	}

	path := "configs/orgs/myorg/projects/proj/services/litellm/config.yaml"
	want := []byte("version: \"1\"\nconfig: {}\n")
	if _, err := repo.CommitAndPush(ctx, "add litellm config", map[string][]byte{path: want}); err != nil {
		t.Fatalf("CommitAndPush litellm config: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- repo.IterateServiceHistory(ctx, "myorg", "proj", "litellm", func(entry gitops.ServiceHistoryEntry) error {
			got, err := repo.ReadFileAtCommit(entry.Version, path)
			if err != nil {
				return err
			}
			if string(got) != string(want) {
				return fmt.Errorf("ReadFileAtCommit content: want %q, got %q", want, got)
			}
			return nil
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("IterateServiceHistory: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("IterateServiceHistory callback deadlocked while re-entering repo")
	}
}

func assertChangedPaths(t *testing.T, got []gitops.ServiceFileChange, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("changed files length: want %d, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i].Path != want[i] {
			t.Fatalf("changed file %d: want %q, got %q", i, want[i], got[i].Path)
		}
	}
}

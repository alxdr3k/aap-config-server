package gitops_test

import (
	"context"
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
		"configs/orgs/org1/projects/proj/services/svc/config.yaml":    []byte("a"),
		"configs/orgs/org1/projects/proj/services/svc/env_vars.yaml":  []byte("b"),
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

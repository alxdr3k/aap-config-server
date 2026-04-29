package gitops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"

	"github.com/aap/config-server/internal/apperror"
)

// isNonFastForwardPush reports whether a go-git push error signals the remote
// rejected the update because it is not a fast-forward. go-git does not wrap
// ErrNonFastForwardUpdate from its push path; the error is a fresh
// fmt.Errorf("non-fast-forward update: %s", refname). We match on the message
// prefix and also accept the sentinel for completeness.
func isNonFastForwardPush(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gogit.ErrNonFastForwardUpdate) {
		return true
	}
	return strings.Contains(err.Error(), "non-fast-forward update")
}

const (
	maxPushRetries  = 3
	committerName   = "aap-config-server"
	committerEmail  = "config-server@aap.internal"
)

// afterPullHook is a test-only hook invoked inside CommitAndPush /
// DeleteAndPush between the pre-operation pull and the worktree mutation.
// Tests use it to inject a competing commit on the remote so the subsequent
// push is rejected non-fast-forward, exercising the retry path.
// It is nil in production.
var afterPullHook func(attempt int)

// GitRepo is the interface the store uses to interact with the git repository.
// All path arguments are relative to the repository root.
type GitRepo interface {
	// CloneOrOpen initializes the local clone (clones if not present, opens otherwise).
	CloneOrOpen(ctx context.Context) error

	// Pull fetches and merges remote changes. Returns the new HEAD hash and
	// whether the HEAD changed.
	Pull(ctx context.Context) (hash string, updated bool, err error)

	// CommitAndPush writes the given files, commits, and pushes.
	// Retries up to 3 times on non-fast-forward push rejection.
	// Returns the new HEAD commit hash.
	CommitAndPush(ctx context.Context, msg string, files map[string][]byte) (string, error)

	// DeleteAndPush removes the listed paths, commits, and pushes.
	// Returns the new HEAD commit hash.
	DeleteAndPush(ctx context.Context, msg string, paths []string) (string, error)

	// ReadFile reads a file from the current working tree.
	ReadFile(path string) ([]byte, error)

	// WalkConfigs calls fn for every regular file under the configs/ subtree.
	// The path argument to fn is relative to the repo root.
	WalkConfigs(fn func(path string, data []byte) error) error

	// Snapshot returns the current HEAD hash together with a walk of every
	// regular file under configs/. The walk is performed under the same lock
	// that serialises Pull / CommitAndPush / DeleteAndPush so the worktree
	// cannot mutate mid-iteration.
	Snapshot(fn func(path string, data []byte) error) (hash string, err error)

	// HeadHash returns the current HEAD commit hash.
	HeadHash() (string, error)

	// ReadFileAtCommit reads a file as it existed at commitHash.
	ReadFileAtCommit(commitHash, path string) ([]byte, error)

	// LocalPath returns the absolute path of the local clone.
	LocalPath() string
}

// Repo is a go-git backed GitRepo implementation.
// A global mutex serialises all git operations to prevent concurrent push
// conflicts (Phase-1 simplification; see ADR-003 for the full design).
type Repo struct {
	mu        sync.Mutex
	repo      *gogit.Repository
	localPath string
	remoteURL string
	branch    string
	auth      transport.AuthMethod
}

// Options configures a Repo.
type Options struct {
	// LocalPath is the filesystem path where the repo is (or will be) cloned.
	LocalPath string
	// RemoteURL is the git remote URL (https or ssh).
	RemoteURL string
	// Branch is the target branch (default "main").
	Branch string
	// SSHKeyPath is the path to an SSH private key file (optional).
	SSHKeyPath string
	// Username / Password for HTTP basic auth (optional).
	Username string
	Password string
}

// New creates a Repo from the given options.
func New(opts Options) (*Repo, error) {
	if opts.Branch == "" {
		opts.Branch = "main"
	}

	auth, err := buildAuth(opts)
	if err != nil {
		return nil, fmt.Errorf("build git auth: %w", err)
	}

	return &Repo{
		localPath: opts.LocalPath,
		remoteURL: opts.RemoteURL,
		branch:    opts.Branch,
		auth:      auth,
	}, nil
}

func buildAuth(opts Options) (transport.AuthMethod, error) {
	if opts.SSHKeyPath != "" {
		keyBytes, err := os.ReadFile(opts.SSHKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read SSH key %s: %w", opts.SSHKeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("parse SSH key: %w", err)
		}
		return &gitssh.PublicKeys{User: "git", Signer: signer}, nil
	}
	if opts.Username != "" || opts.Password != "" {
		return &githttp.BasicAuth{Username: opts.Username, Password: opts.Password}, nil
	}
	return nil, nil
}

// CloneOrOpen clones the repository if the local path is empty, or opens it.
func (r *Repo) CloneOrOpen(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, err := os.Stat(filepath.Join(r.localPath, ".git")); err == nil {
		repo, err := gogit.PlainOpen(r.localPath)
		if err != nil {
			return fmt.Errorf("open repo at %s: %w", r.localPath, err)
		}
		r.repo = repo
		slog.Info("opened existing git repo", "path", r.localPath)
		return nil
	}

	if err := os.MkdirAll(r.localPath, 0o755); err != nil {
		return fmt.Errorf("create local path %s: %w", r.localPath, err)
	}

	repo, err := gogit.PlainCloneContext(ctx, r.localPath, false, &gogit.CloneOptions{
		URL:           r.remoteURL,
		ReferenceName: plumbing.NewBranchReferenceName(r.branch),
		SingleBranch:  true,
		Auth:          r.auth,
	})
	if err != nil {
		return fmt.Errorf("clone %s: %w", r.remoteURL, err)
	}
	r.repo = repo
	slog.Info("cloned git repo", "url", r.remoteURL, "path", r.localPath)
	return nil
}

// Pull fetches and merges remote changes.
func (r *Repo) Pull(ctx context.Context) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	before, err := r.headHash()
	if err != nil {
		return "", false, err
	}

	if err := r.pull(ctx); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return "", false, fmt.Errorf("pull: %w", err)
	}

	after, err := r.headHash()
	if err != nil {
		return "", false, err
	}
	return after, before != after, nil
}

func (r *Repo) pull(ctx context.Context) error {
	w, err := r.repo.Worktree()
	if err != nil {
		return err
	}
	return w.PullContext(ctx, &gogit.PullOptions{
		RemoteName:    "origin",
		ReferenceName: plumbing.NewBranchReferenceName(r.branch),
		Auth:          r.auth,
	})
}

// CommitAndPush writes files, commits, and pushes with retry on rejection.
func (r *Repo) CommitAndPush(ctx context.Context, msg string, files map[string][]byte) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for attempt := 0; attempt < maxPushRetries; attempt++ {
		// Sync with remote before writing.
		if err := r.pull(ctx); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
			return "", fmt.Errorf("pre-commit pull: %w", err)
		}
		if afterPullHook != nil {
			afterPullHook(attempt)
		}

		w, err := r.repo.Worktree()
		if err != nil {
			return "", err
		}

		// Write files to working tree and stage them.
		for path, data := range files {
			fullPath := filepath.Join(r.localPath, path)
			if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
				return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(fullPath), err)
			}
			if err := os.WriteFile(fullPath, data, 0o644); err != nil {
				return "", fmt.Errorf("write %s: %w", path, err)
			}
			if _, err := w.Add(path); err != nil {
				return "", fmt.Errorf("git add %s: %w", path, err)
			}
		}

		hash, err := w.Commit(msg, &gogit.CommitOptions{
			Author: signature(),
		})
		if err != nil {
			if errors.Is(err, gogit.ErrEmptyCommit) {
				h, _ := r.headHash()
				return h, nil
			}
			return "", fmt.Errorf("git commit: %w", err)
		}

		pushErr := r.repo.PushContext(ctx, &gogit.PushOptions{
			RemoteName: "origin",
			Auth:       r.auth,
		})
		if pushErr == nil {
			return hash.String(), nil
		}
		if !isNonFastForwardPush(pushErr) {
			return "", apperror.Wrap(apperror.CodeGitPush, "push failed", pushErr)
		}

		// Push rejected by remote: undo the local commit so the next loop
		// iteration starts clean from a pull.
		slog.Warn("git push rejected, will retry after pull", "attempt", attempt+1)
		if err := resetToParent(r.repo, w); err != nil {
			return "", fmt.Errorf("reset after push rejection: %w", err)
		}
	}

	return "", apperror.New(apperror.CodeGitPush, fmt.Sprintf("push failed after %d retries", maxPushRetries))
}

// DeleteAndPush removes the listed paths, commits, and pushes.
func (r *Repo) DeleteAndPush(ctx context.Context, msg string, paths []string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for attempt := 0; attempt < maxPushRetries; attempt++ {
		if err := r.pull(ctx); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
			return "", fmt.Errorf("pre-delete pull: %w", err)
		}
		if afterPullHook != nil {
			afterPullHook(attempt)
		}

		w, err := r.repo.Worktree()
		if err != nil {
			return "", err
		}

		for _, path := range paths {
			fullPath := filepath.Join(r.localPath, path)
			if err := os.RemoveAll(fullPath); err != nil && !os.IsNotExist(err) {
				return "", fmt.Errorf("remove %s: %w", path, err)
			}
			// go-git: Remove stages the deletion regardless of whether the file
			// existed on disk.
			if _, err := w.Remove(path); err != nil && !errors.Is(err, object.ErrEntryNotFound) {
				slog.Debug("git remove warning (non-fatal)", "path", path, "err", err)
			}
		}

		hash, err := w.Commit(msg, &gogit.CommitOptions{
			Author:            signature(),
			AllowEmptyCommits: false,
		})
		if err != nil {
			if errors.Is(err, gogit.ErrEmptyCommit) {
				// Nothing was actually deleted — treat as no-op success.
				h, _ := r.headHash()
				return h, nil
			}
			return "", fmt.Errorf("git commit delete: %w", err)
		}

		pushErr := r.repo.PushContext(ctx, &gogit.PushOptions{
			RemoteName: "origin",
			Auth:       r.auth,
		})
		if pushErr == nil {
			return hash.String(), nil
		}
		if !isNonFastForwardPush(pushErr) {
			return "", apperror.Wrap(apperror.CodeGitPush, "push failed", pushErr)
		}

		slog.Warn("git push (delete) rejected, will retry", "attempt", attempt+1)
		if err := resetToParent(r.repo, w); err != nil {
			return "", fmt.Errorf("reset after push rejection: %w", err)
		}
	}

	return "", apperror.New(apperror.CodeGitPush, fmt.Sprintf("push (delete) failed after %d retries", maxPushRetries))
}

// ReadFile reads a file from the current working tree.
func (r *Repo) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(r.localPath, path))
}

// WalkConfigs iterates over all regular files under configs/.
// It acquires the repo lock for the full walk so concurrent Pull /
// CommitAndPush cannot mutate the worktree mid-iteration.
func (r *Repo) WalkConfigs(fn func(path string, data []byte) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.walkConfigsUnlocked(fn)
}

func (r *Repo) walkConfigsUnlocked(fn func(path string, data []byte) error) error {
	configsRoot := filepath.Join(r.localPath, "configs")
	if _, err := os.Stat(configsRoot); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(configsRoot, func(absPath string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(r.localPath, absPath)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		return fn(rel, data)
	})
}

// Snapshot returns (hash, walk-err) with HeadHash and WalkConfigs performed
// under the same repo lock so reads never straddle a pull or commit.
//
// Snapshot also refuses to build a view over a dirty configs/ worktree: if
// any file under configs/ is modified, added, deleted, or untracked (outside
// of our own locked write paths), the reported HEAD hash would not describe
// what we're about to serve. Returning an error here fails the reload closed,
// which the store then reports via /readyz degraded and /api/v1/status.
func (r *Repo) Snapshot(fn func(path string, data []byte) error) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	hash, err := r.headHash()
	if err != nil {
		return "", err
	}
	if err := r.assertConfigsCleanUnlocked(); err != nil {
		return "", err
	}
	if err := r.walkConfigsUnlocked(fn); err != nil {
		return "", err
	}
	return hash, nil
}

// assertConfigsCleanUnlocked returns an error if any file under configs/ is
// in a non-clean git state. CommitAndPush / DeleteAndPush hold the same lock
// while mutating the worktree, so this check only ever fires on changes made
// outside the process — i.e. drift on the local checkout that would make the
// served snapshot diverge from the reported HEAD.
func (r *Repo) assertConfigsCleanUnlocked() error {
	w, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	status, err := w.Status()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	var dirty []string
	for path, st := range status {
		if !strings.HasPrefix(filepath.ToSlash(path), "configs/") {
			continue
		}
		// Ignore Unmodified entries (go-git's Status map can include them).
		if st.Staging == gogit.Unmodified && st.Worktree == gogit.Unmodified {
			continue
		}
		dirty = append(dirty, path)
	}
	if len(dirty) > 0 {
		// Cap the list in the error so a huge dirty worktree doesn't produce
		// an unreadably long message.
		const max = 5
		shown := dirty
		if len(shown) > max {
			shown = shown[:max]
		}
		return fmt.Errorf("configs/ worktree is dirty (%d file(s), e.g. %s); refusing to snapshot",
			len(dirty), strings.Join(shown, ", "))
	}
	return nil
}

// HeadHash returns the current HEAD commit hash.
func (r *Repo) HeadHash() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headHash()
}

func (r *Repo) headHash() (string, error) {
	ref, err := r.repo.Head()
	if err != nil {
		return "", fmt.Errorf("git head: %w", err)
	}
	return ref.Hash().String(), nil
}

// ReadFileAtCommit reads a file as it existed at the given commit hash.
func (r *Repo) ReadFileAtCommit(commitHash, path string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	h := plumbing.NewHash(commitHash)
	commit, err := r.repo.CommitObject(h)
	if err != nil {
		return nil, fmt.Errorf("commit %s not found: %w", commitHash, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}
	file, err := tree.File(path)
	if err != nil {
		return nil, fmt.Errorf("file %s at %s: %w", path, commitHash, err)
	}
	content, err := file.Contents()
	if err != nil {
		return nil, err
	}
	return []byte(content), nil
}

// LocalPath returns the absolute filesystem path of the local clone.
func (r *Repo) LocalPath() string { return r.localPath }

// resetToParent does a hard reset to HEAD~1. It undoes the rejected local
// commit AND discards the staged working-tree changes, so the next retry
// iteration starts from a clean tree before pulling the remote's newer
// commits. Files are re-applied by the retry loop itself.
func resetToParent(repo *gogit.Repository, w *gogit.Worktree) error {
	head, err := repo.Head()
	if err != nil {
		return err
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return err
	}
	parents := commit.Parents()
	parent, err := parents.Next()
	if err != nil {
		return fmt.Errorf("get parent commit: %w", err)
	}
	return w.Reset(&gogit.ResetOptions{
		Commit: parent.Hash,
		Mode:   gogit.HardReset,
	})
}

func signature() *object.Signature {
	return &object.Signature{
		Name:  committerName,
		Email: committerEmail,
		When:  time.Now(),
	}
}

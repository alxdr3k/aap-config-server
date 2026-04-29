package secret

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// FileVolumeReader reads plaintext K8s Secret values from a directory mount.
// The expected layout is:
//
//	{mountPath}/{namespace}/{secretName}/{key}
type FileVolumeReader struct {
	mountPath string

	mu    sync.RWMutex
	cache map[Reference]Value
}

// NewFileVolumeReader creates a mounted-secret reader rooted at mountPath.
func NewFileVolumeReader(mountPath string) (*FileVolumeReader, error) {
	if mountPath == "" {
		return nil, errors.New("secret mount path is required")
	}
	if !filepath.IsAbs(mountPath) {
		return nil, fmt.Errorf("secret mount path must be absolute, got %q", mountPath)
	}
	return &FileVolumeReader{
		mountPath: filepath.Clean(mountPath),
		cache:     map[Reference]Value{},
	}, nil
}

// Read returns a cached secret value, loading it from the mounted file on the
// first read. Use Refresh to force a re-read after a known filesystem change.
func (r *FileVolumeReader) Read(ctx context.Context, ref Reference) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := validateReference(ref); err != nil {
		return Value{}, err
	}

	r.mu.RLock()
	v, ok := r.cache[ref]
	if ok {
		out := NewValue(v.Bytes())
		r.mu.RUnlock()
		return out, nil
	}
	r.mu.RUnlock()

	return r.Refresh(ctx, ref)
}

// Refresh re-reads a mounted secret file and updates the cached value.
func (r *FileVolumeReader) Refresh(ctx context.Context, ref Reference) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	path, err := r.Path(ref)
	if err != nil {
		return Value{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Value{}, fmt.Errorf("read mounted secret %s/%s/%s: %w",
			ref.Namespace, ref.Name, ref.Key, err)
	}
	defer zeroBytes(raw)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}

	next := NewValue(raw)
	r.mu.Lock()
	if old, ok := r.cache[ref]; ok {
		old.Destroy()
	}
	r.cache[ref] = next
	out := NewValue(next.Bytes())
	r.mu.Unlock()

	return out, nil
}

// Forget removes a cached value, zeroing retained bytes on a best-effort basis.
func (r *FileVolumeReader) Forget(ref Reference) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.cache[ref]; ok {
		old.Destroy()
		delete(r.cache, ref)
	}
}

// Path returns the filesystem path for ref under the reader's mount root.
func (r *FileVolumeReader) Path(ref Reference) (string, error) {
	if err := validateReference(ref); err != nil {
		return "", err
	}
	path := filepath.Join(r.mountPath, ref.Namespace, ref.Name, ref.Key)
	rel, err := filepath.Rel(r.mountPath, path)
	if err != nil {
		return "", fmt.Errorf("resolve mounted secret path: %w", err)
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("mounted secret path escapes mount root: %s/%s/%s",
			ref.Namespace, ref.Name, ref.Key)
	}
	return path, nil
}

// Watch watches parent directories for mounted secret file updates. Kubernetes
// secret volumes update through symlink swaps, so parent-directory watches are
// more reliable than watching individual files.
func (r *FileVolumeReader) Watch(ctx context.Context, refs []Reference) (<-chan VolumeEvent, error) {
	if len(refs) == 0 {
		return nil, errors.New("at least one secret reference is required")
	}

	paths := make(map[string][]Reference, len(refs))
	byDir := map[string][]Reference{}
	for _, ref := range refs {
		path, err := r.Path(ref)
		if err != nil {
			return nil, err
		}
		clean := filepath.Clean(path)
		paths[clean] = append(paths[clean], ref)
		dir := filepath.Dir(clean)
		byDir[dir] = append(byDir[dir], ref)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create mounted secret watcher: %w", err)
	}
	for dir := range byDir {
		if err := watcher.Add(dir); err != nil {
			_ = watcher.Close()
			return nil, fmt.Errorf("watch mounted secret dir %s: %w", dir, err)
		}
	}

	out := make(chan VolumeEvent, len(refs))
	go r.watchLoop(ctx, watcher, paths, byDir, out)
	return out, nil
}

func (r *FileVolumeReader) watchLoop(
	ctx context.Context,
	watcher *fsnotify.Watcher,
	paths map[string][]Reference,
	byDir map[string][]Reference,
	out chan<- VolumeEvent,
) {
	defer close(out)
	defer func() { _ = watcher.Close() }()

	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			sendVolumeEvent(ctx, out, VolumeEvent{Err: err})
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			r.handleWatchEvent(ctx, event, paths, byDir, out)
		}
	}
}

func (r *FileVolumeReader) handleWatchEvent(
	ctx context.Context,
	event fsnotify.Event,
	paths map[string][]Reference,
	byDir map[string][]Reference,
	out chan<- VolumeEvent,
) {
	clean := filepath.Clean(event.Name)
	if refs, ok := paths[clean]; ok {
		for _, ref := range refs {
			r.refreshFromEvent(ctx, ref, clean, classifyVolumeOp(event.Op), out)
		}
		return
	}
	for _, ref := range byDir[clean] {
		path, err := r.Path(ref)
		if err != nil {
			sendVolumeEvent(ctx, out, VolumeEvent{Reference: ref, Path: clean, Err: err})
			continue
		}
		r.refreshFromEvent(ctx, ref, path, classifyVolumeOp(event.Op), out)
	}
	if refs := byDir[filepath.Dir(clean)]; len(refs) > 0 {
		for _, ref := range refs {
			path, err := r.Path(ref)
			if err != nil {
				sendVolumeEvent(ctx, out, VolumeEvent{Reference: ref, Path: clean, Err: err})
				continue
			}
			r.refreshFromEvent(ctx, ref, path, classifyVolumeOp(event.Op), out)
		}
	}
}

func (r *FileVolumeReader) refreshFromEvent(
	ctx context.Context,
	ref Reference,
	path string,
	op VolumeOp,
	out chan<- VolumeEvent,
) {
	ev := VolumeEvent{Reference: ref, Path: path, Op: op}
	if op == VolumeOpRemove {
		r.Forget(ref)
		sendVolumeEvent(ctx, out, ev)
		return
	}
	_, ev.Err = r.Refresh(ctx, ref)
	sendVolumeEvent(ctx, out, ev)
}

func classifyVolumeOp(op fsnotify.Op) VolumeOp {
	if op&fsnotify.Remove != 0 {
		return VolumeOpRemove
	}
	return VolumeOpWrite
}

func sendVolumeEvent(ctx context.Context, out chan<- VolumeEvent, ev VolumeEvent) {
	select {
	case <-ctx.Done():
	case out <- ev:
	}
}

func validateReference(ref Reference) error {
	if ref.ID == "" {
		return errors.New("secret id is required")
	}
	if err := validatePathSegment("namespace", ref.Namespace); err != nil {
		return err
	}
	if err := validatePathSegment("name", ref.Name); err != nil {
		return err
	}
	if err := validatePathSegment("key", ref.Key); err != nil {
		return err
	}
	return nil
}

func validatePathSegment(field, value string) error {
	if value == "" {
		return fmt.Errorf("secret %s is required", field)
	}
	if value == "." || value == ".." || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("secret %s %q contains invalid path characters", field, value)
	}
	return nil
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

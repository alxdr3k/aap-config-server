package secret_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aap/config-server/internal/secret"
)

func TestFileVolumeReader_ReadAndRefresh(t *testing.T) {
	root := t.TempDir()
	ref := testReference()
	path := writeMountedSecret(t, root, ref, "initial")

	reader, err := secret.NewFileVolumeReader(root)
	if err != nil {
		t.Fatalf("NewFileVolumeReader: %v", err)
	}

	v, err := reader.Read(context.Background(), ref)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(v.Bytes()) != "initial" {
		t.Fatalf("initial read: got %q", string(v.Bytes()))
	}

	if err := os.WriteFile(path, []byte("rotated"), 0o600); err != nil {
		t.Fatalf("write rotated secret: %v", err)
	}

	cached, err := reader.Read(context.Background(), ref)
	if err != nil {
		t.Fatalf("Read cached: %v", err)
	}
	if string(cached.Bytes()) != "initial" {
		t.Fatalf("Read should return cached value before Refresh, got %q", string(cached.Bytes()))
	}

	refreshed, err := reader.Refresh(context.Background(), ref)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if string(refreshed.Bytes()) != "rotated" {
		t.Fatalf("refreshed value: got %q", string(refreshed.Bytes()))
	}
}

func TestFileVolumeReader_RejectsUnsafePaths(t *testing.T) {
	reader, err := secret.NewFileVolumeReader(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileVolumeReader: %v", err)
	}

	tests := []secret.Reference{
		{ID: "", Namespace: "ns", Name: "name", Key: "key"},
		{ID: "id", Namespace: "..", Name: "name", Key: "key"},
		{ID: "id", Namespace: "ns/other", Name: "name", Key: "key"},
		{ID: "id", Namespace: "ns", Name: "name", Key: "../key"},
	}
	for _, ref := range tests {
		if _, err := reader.Path(ref); err == nil {
			t.Fatalf("Path(%+v): expected error", ref)
		}
	}

	if _, err := secret.NewFileVolumeReader("relative"); err == nil {
		t.Fatal("relative mount path should be rejected")
	}
}

func TestFileVolumeReader_WatchRefreshesChangedFile(t *testing.T) {
	root := t.TempDir()
	ref := testReference()
	path := writeMountedSecret(t, root, ref, "initial")

	reader, err := secret.NewFileVolumeReader(root)
	if err != nil {
		t.Fatalf("NewFileVolumeReader: %v", err)
	}
	if _, err := reader.Refresh(context.Background(), ref); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := reader.Watch(ctx, []secret.Reference{ref})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	if err := os.WriteFile(path, []byte("rotated"), 0o600); err != nil {
		t.Fatalf("write rotated secret: %v", err)
	}

	ev := waitForVolumeEvent(t, events, ref)
	if ev.Err != nil {
		t.Fatalf("watch event error: %v", ev.Err)
	}
	if ev.Op != secret.VolumeOpWrite {
		t.Fatalf("event op: want %q, got %q", secret.VolumeOpWrite, ev.Op)
	}

	got, err := reader.Read(context.Background(), ref)
	if err != nil {
		t.Fatalf("Read after watch: %v", err)
	}
	if string(got.Bytes()) != "rotated" {
		t.Fatalf("watch should refresh cache, got %q", string(got.Bytes()))
	}
}

func TestFileVolumeReader_WatchRefreshesDuplicatePathReferences(t *testing.T) {
	root := t.TempDir()
	refA := testReference()
	refB := refA
	refB.ID = "shared-api-key"
	path := writeMountedSecret(t, root, refA, "initial")

	reader, err := secret.NewFileVolumeReader(root)
	if err != nil {
		t.Fatalf("NewFileVolumeReader: %v", err)
	}
	for _, ref := range []secret.Reference{refA, refB} {
		if _, err := reader.Refresh(context.Background(), ref); err != nil {
			t.Fatalf("initial refresh %s: %v", ref.ID, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := reader.Watch(ctx, []secret.Reference{refA, refB})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	if err := os.WriteFile(path, []byte("rotated"), 0o600); err != nil {
		t.Fatalf("write rotated secret: %v", err)
	}

	seen := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(seen) < 2 {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("watch channel closed before duplicate ref events")
			}
			if ev.Err != nil {
				t.Fatalf("watch event error: %v", ev.Err)
			}
			seen[ev.Reference.ID] = true
		case <-deadline:
			t.Fatalf("timed out waiting for duplicate ref events, saw %v", seen)
		}
	}

	for _, ref := range []secret.Reference{refA, refB} {
		got, err := reader.Read(context.Background(), ref)
		if err != nil {
			t.Fatalf("Read after watch %s: %v", ref.ID, err)
		}
		if string(got.Bytes()) != "rotated" {
			t.Fatalf("watch should refresh cache for %s, got %q", ref.ID, string(got.Bytes()))
		}
	}
}

func TestFileVolumeReader_ConcurrentReadRefresh(t *testing.T) {
	root := t.TempDir()
	ref := testReference()
	path := writeMountedSecret(t, root, ref, "initial")

	reader, err := secret.NewFileVolumeReader(root)
	if err != nil {
		t.Fatalf("NewFileVolumeReader: %v", err)
	}
	if _, err := reader.Refresh(context.Background(), ref); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, err := reader.Read(context.Background(), ref); err != nil {
					t.Errorf("Read: %v", err)
					return
				}
			}
		}()
	}

	for i := 0; i < 50; i++ {
		if err := os.WriteFile(path, []byte("rotated"), 0o600); err != nil {
			t.Fatalf("write rotated secret: %v", err)
		}
		if _, err := reader.Refresh(context.Background(), ref); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	}
	wg.Wait()
}

func testReference() secret.Reference {
	return secret.Reference{
		ID:        "api-key",
		Namespace: "ai-platform",
		Name:      "provider-keys",
		Key:       "azure",
	}
}

func writeMountedSecret(t *testing.T, root string, ref secret.Reference, value string) string {
	t.Helper()
	path := filepath.Join(root, ref.Namespace, ref.Name, ref.Key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir mounted secret dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatalf("write mounted secret: %v", err)
	}
	return path
}

func waitForVolumeEvent(t *testing.T, events <-chan secret.VolumeEvent, ref secret.Reference) secret.VolumeEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("watch channel closed before event")
			}
			if ev.Reference == ref || (ev.Err != nil && strings.Contains(ev.Err.Error(), ref.Key)) {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for volume watch event")
		}
	}
}

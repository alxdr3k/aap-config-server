package secret

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestHandleWatchEvent_TreatsHousekeepingRemoveAsRefresh(t *testing.T) {
	root := t.TempDir()
	ref := Reference{
		ID:        "api-key",
		Namespace: "ai-platform",
		Name:      "provider-keys",
		Key:       "azure",
	}
	path := filepath.Join(root, ref.Namespace, ref.Name, ref.Key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir mounted secret dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("initial"), 0o600); err != nil {
		t.Fatalf("write mounted secret: %v", err)
	}

	reader, err := NewFileVolumeReader(root)
	if err != nil {
		t.Fatalf("NewFileVolumeReader: %v", err)
	}
	if _, err := reader.Refresh(context.Background(), ref); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	if err := os.WriteFile(path, []byte("rotated"), 0o600); err != nil {
		t.Fatalf("write rotated secret: %v", err)
	}

	events := make(chan VolumeEvent, 1)
	reader.handleWatchEvent(
		context.Background(),
		fsnotify.Event{Name: filepath.Join(filepath.Dir(path), "..2026_04_29_09_55_00.000000001"), Op: fsnotify.Remove},
		map[string][]Reference{path: {ref}},
		map[string][]Reference{filepath.Dir(path): {ref}},
		events,
	)

	ev := <-events
	if ev.Err != nil {
		t.Fatalf("watch event error: %v", ev.Err)
	}
	if ev.Op != VolumeOpWrite {
		t.Fatalf("housekeeping remove should refresh, got op %q", ev.Op)
	}

	got, err := reader.Read(context.Background(), ref)
	if err != nil {
		t.Fatalf("Read after housekeeping remove: %v", err)
	}
	if string(got.Bytes()) != "rotated" {
		t.Fatalf("housekeeping remove should refresh cache, got %q", string(got.Bytes()))
	}
}

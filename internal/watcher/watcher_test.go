package watcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestFindProjectRootUsesPathBoundaries(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	sibling := filepath.Join(base, "project-other")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}

	inside := filepath.Join(root, "doc.md")
	if got := findProjectRoot(inside, []string{root}); got != root {
		t.Fatalf("expected %q, got %q", root, got)
	}

	outside := filepath.Join(sibling, "doc.md")
	if got := findProjectRoot(outside, []string{root}); got != "" {
		t.Fatalf("expected sibling path not to match root, got %q", got)
	}
}

func TestWatchWithContextEmitsMarkdownChanges(t *testing.T) {
	root := t.TempDir()
	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type change struct {
		projectPath string
		files       []string
	}
	changes := make(chan change, 4)
	errs := make(chan error, 1)
	go func() {
		errs <- WatchWithContext(ctx, []string{root}, 25*time.Millisecond, func(projectPath string, files []string) {
			changes <- change{projectPath: projectPath, files: files}
		})
	}()

	// Give fsnotify time to register the temporary directory before writing.
	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("# Doc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-changes:
		if got.projectPath != absRoot {
			t.Fatalf("expected project path %q, got %q", absRoot, got.projectPath)
		}
		if !contains(got.files, "doc.md") {
			t.Fatalf("expected doc.md in changed files, got %v", got.files)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watcher callback")
	}

	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watcher shutdown")
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// TestWatchRecoversFromPanickingCallback proves the watcher survives a panic in
// the reindex callback (security-audit backlog #4). The callback runs on the
// time.AfterFunc timer goroutine, which has no parent recover — without the
// per-project recover in WatchWithContext, a panicking reindex (e.g. a
// malformed document) would kill the whole serve process. The first change
// triggers a panic; the watcher must keep running and still deliver a later
// change.
func TestWatchRecoversFromPanickingCallback(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls int32
	delivered := make(chan string, 10)
	go func() {
		_ = WatchWithContext(ctx, []string{dir}, 100*time.Millisecond, func(projectPath string, files []string) {
			// Panic on the first batch only; deliver on every later batch.
			if atomic.AddInt32(&calls, 1) == 1 {
				panic("simulated reindex panic from a malformed document")
			}
			for _, f := range files {
				delivered <- f
			}
		})
	}()

	// Give fsnotify time to register the temporary directory before writing.
	time.Sleep(150 * time.Millisecond)

	// First change → callback panics; the AfterFunc goroutine must recover
	// rather than crash the test process. If the recover is missing, the
	// unrecovered panic terminates the whole test binary.
	if err := os.WriteFile(filepath.Join(dir, "first.md"), []byte("# First"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for the panicking batch to actually fire before writing again, so the
	// two changes don't coalesce into a single debounce batch.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&calls) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first (panicking) callback never fired")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Second change → the watcher must still be alive and deliver it.
	if err := os.WriteFile(filepath.Join(dir, "second.md"), []byte("# Second"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-delivered:
		// Watcher survived the panic and kept delivering — recover works.
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not deliver events after a panicking callback (recover missing?)")
	}
}

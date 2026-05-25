package watcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

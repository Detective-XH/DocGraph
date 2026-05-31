package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// TestWatchBudgetGatesDescent verifies the watch budget stops the recursive walk
// once the cap is reached — the cross-platform half of the fd-exhaustion
// regression guard. The gating logic (count every entry, SkipDir at the cap) is
// identical on inotify and kqueue even though only kqueue makes each entry cost
// an fd, so this runs on the Linux CI lane where TestWatcherBoundsWatchFdCount
// (which measures real fds) skips. Without the cap, addRecursive would walk and
// register every one of the tree's entries.
func TestWatchBudgetGatesDescent(t *testing.T) {
	root := t.TempDir()
	total := buildBalancedTree(t, root, 2, 9, 2) // ≈3000 entries
	if total < 2000 {
		t.Fatalf("fixture too small: %d entries", total)
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	const cap = 500
	b := &watchBudget{max: cap}
	_ = addRecursive(w, root, b)

	got := b.count()
	// Narrow fixture (fanout 2) keeps per-directory overshoot tiny, so the walk
	// halts just past the cap rather than near the tree's ~3000 entries.
	if got > cap+50 {
		t.Fatalf("budget did not gate descent: walked %d entries with cap %d (tree=%d)", got, cap, total)
	}
	if got < cap/2 {
		t.Fatalf("budget stopped suspiciously early: %d entries with cap %d", got, cap)
	}
}

// openFDCount approximates the number of file descriptors currently open in this
// process: the kernel assigns the lowest-available descriptor, so a freshly
// opened fd's integer value ≈ the live open-fd count. The delta across an action
// isolates that action's fd cost without parsing lsof or /proc (absent on macOS).
func openFDCount(t *testing.T) int {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	n := int(f.Fd())
	f.Close()
	return n
}

// buildBalancedTree writes a bounded-fanout directory tree and returns the total
// entry count (dirs + files). Bounded fanout keeps each directory narrow, which
// matters for the watch-cap test: fsnotify's kqueue backend eagerly opens an fd
// for every immediate entry of a directory the instant it is watched, so a wide
// directory would let watched-fd count overshoot the budget by its width. A
// narrow tree keeps that overshoot tiny, so the cap binds tightly.
func buildBalancedTree(t *testing.T, dir string, fanout, depth, filesPerDir int) int {
	t.Helper()
	count := 0
	for f := 0; f < filesPerDir; f++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.md", f)), []byte("# x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if depth == 0 {
		return count
	}
	for c := 0; c < fanout; c++ {
		sub := filepath.Join(dir, fmt.Sprintf("c%d", c))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		count++
		count += buildBalancedTree(t, sub, fanout, depth-1, filesPerDir)
	}
	return count
}

// watchSettledFDDelta starts a watcher over root with the given cap, lets the
// background recursive walk settle, and returns the open-fd delta it produced.
func watchSettledFDDelta(t *testing.T, root string, maxWatches int) int {
	t.Helper()
	before := openFDCount(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = WatchWithContext(ctx, []string{root}, time.Second, maxWatches, func(string, []string) {})
		close(done)
	}()
	time.Sleep(2 * time.Second) // let the background addRecursive walk complete
	delta := openFDCount(t) - before
	cancel()
	<-done
	time.Sleep(200 * time.Millisecond) // let fsnotify release fds (defer w.Close)
	return delta
}

// TestWatcherBoundsWatchFdCount is the regression guard for the serve --workspace
// file-descriptor exhaustion (plans/serve-workspace-fd-exhaustion). On macOS
// fsnotify (kqueue) opens one fd per watched directory and file, so recursively
// watching a large workspace grows open fds linearly with tree size — measured at
// ~58K fds per serve process on a 120GB workspace, which (multiplied across every
// concurrent serve) exhausted the system file table. The per-process watch budget
// must hold fd growth to O(cap) regardless of tree size. The test proves both:
// the uncapped walk scales with the tree (the bug), while the capped walk plateaus
// near the cap (the fix).
func TestWatcherBoundsWatchFdCount(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("one-fd-per-watch accounting is kqueue/macOS-specific")
	}
	root := t.TempDir()
	// fanout 2, depth 9, 2 files/dir ≈ 3000 entries — well above the test cap.
	total := buildBalancedTree(t, root, 2, 9, 2)
	if total < 2000 {
		t.Fatalf("fixture too small: %d entries", total)
	}

	uncapped := watchSettledFDDelta(t, root, 0)
	const cap = 500
	capped := watchSettledFDDelta(t, root, cap)

	t.Logf("tree=%d entries  uncapped fd delta=%d  capped(%d) fd delta=%d", total, uncapped, cap, capped)

	// The bug: without the cap, watched fds scale with the tree.
	if uncapped < 1500 {
		t.Fatalf("expected uncapped watch to open >=1500 fds on a %d-entry tree, got %d", total, uncapped)
	}
	// The fix: with the cap, fd growth plateaus near the cap (small slack for the
	// kqueue control fd, the close pipe, and narrow per-directory overshoot).
	if capped > cap+150 {
		t.Fatalf("capped watch fd delta %d exceeded budget %d (+slack); cap not enforced", capped, cap)
	}
	if capped >= uncapped {
		t.Fatalf("cap did not reduce fd usage: capped=%d uncapped=%d", capped, uncapped)
	}
}

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
		errs <- WatchWithContext(ctx, []string{root}, 25*time.Millisecond, 0, func(projectPath string, files []string) {
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
		_ = WatchWithContext(ctx, []string{dir}, 100*time.Millisecond, 0, func(projectPath string, files []string) {
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

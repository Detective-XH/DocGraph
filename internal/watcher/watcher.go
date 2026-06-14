package watcher

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/Detective-XH/docgraph/internal/docformat"
)

type OnChangeFunc func(projectPath string, changedFiles []string)

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "target": true, "dist": true,
	"build": true, ".codegraph": true, ".docgraph": true, ".next": true,
	".cache": true, "vendor": true, "__pycache__": true, ".obsidian": true,
}

// DefaultMaxWatches bounds how many filesystem entries (directories + files) a
// single serve process registers with fsnotify. On macOS (kqueue) every watched
// entry costs one open file descriptor, so recursively watching a large
// workspace can push the process — and, multiplied across every concurrent serve
// (one per connected MCP client), the whole machine — into file-table exhaustion
// (ENFILE). Bounding the walk trades complete coverage of an oversized tree for a
// bound on fd usage: roughly the cap, plus the widest single watched directory
// (fsnotify opens an fd for every immediate entry the instant a directory is
// added, before the budget is re-checked, so one very wide directory can overrun
// the cap by its width). It is a soft ceiling, not a hard guarantee — a hard one
// would need patching fsnotify. A stale tail re-syncs on the next `docgraph sync`
// or restart. 0 disables the bound. See plans/serve-workspace-fd-exhaustion.
const DefaultMaxWatches = 8192

// watchBudget caps the number of fsnotify watches a serve process holds. It is
// shared (by pointer) across the initial recursive walk and every later
// Create-driven addRecursive, all of which run concurrently — hence the atomic
// counter and the once-guarded warning. The counter is monotonic (it does not
// decrement when fsnotify auto-releases watches on deletion), so once it fills —
// e.g. after the initial walk of an oversized tree — later Create-driven
// addRecursive calls are no-ops and newly-created directories are not watched
// until a restart. That is the intended degradation for an oversized tree, but
// it also means a long-lived serve on a near-cap tree with heavy churn can ratchet
// to the cap and stop auto-reindexing; `docgraph sync` or a restart recovers it.
type watchBudget struct {
	max  int // 0 = unlimited
	n    atomic.Int64
	warn sync.Once
}

func (b *watchBudget) full() bool { return b.max > 0 && b.n.Load() >= int64(b.max) }
func (b *watchBudget) inc()       { b.n.Add(1) }
func (b *watchBudget) count() int { return int(b.n.Load()) }
func (b *watchBudget) warnOnce() {
	b.warn.Do(func() {
		log.Printf("watcher: reached watch limit %d (workspace too large to fully watch); "+
			"changes outside watched directories will not auto-reindex — run `docgraph sync` or raise --max-watches", b.max)
	})
}

func Watch(paths []string, onChange OnChangeFunc) error {
	return WatchWithLimit(paths, DefaultMaxWatches, onChange)
}

// WatchWithLimit is Watch with an explicit per-process watch cap (see
// watchBudget / DefaultMaxWatches). 0 disables the cap.
func WatchWithLimit(paths []string, maxWatches int, onChange OnChangeFunc) error {
	return WatchWithContext(context.Background(), paths, 2*time.Second, maxWatches, onChange)
}

// addRoots resolves each path to an absolute path, registers it synchronously
// with the watcher, and returns the list of absolute root paths. Subdirectory
// expansion is deferred to the caller (run in a background goroutine via
// addRecursive). The root's own fd is counted by the addRecursive walk (its
// first visit re-Adds the root idempotently), so it is not double-counted here.
func addRoots(w *fsnotify.Watcher, paths []string) ([]string, error) {
	roots := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("abs path %s: %w", p, err)
		}
		// Add only the root synchronously so the event loop can start immediately.
		// Subdirectory expansion runs in the background; new dirs created after
		// startup are handled by the Create-event handler below.
		if err := w.Add(abs); err != nil {
			return nil, fmt.Errorf("watch %s: %w", abs, err)
		}
		roots = append(roots, abs)
	}
	return roots, nil
}

// classifyWatchEvent is a pure filter that decides whether a filesystem event
// should be forwarded to onChange. It returns the project root and relative path
// on success (ok=true). It has no side effects — all stateful operations
// (Create-dir addRecursive, debounce scheduling) remain in the caller.
//
// Ordering invariant: the caller MUST invoke handleCreateDir BEFORE calling this
// function. A newly-created directory fails the supported-ext check (classify →
// ok=false → continue), so if the addRecursive side-effect ran after classify it
// would never execute for directory Create events.
func classifyWatchEvent(name string, roots []string) (root, rel string, ok bool) {
	base := filepath.Base(name)
	isIgnoreRuleFile := base == ".docgraphignore" || base == ".gitignore"
	// Ignore-rule files have no supported extension, so the check below would
	// drop them — but editing one changes which files are in scope. Let their
	// events through (as a reindex trigger; the rel path is forwarded to
	// onChange, which re-scans the whole project and runs the ignore-aware
	// reconcile that prunes any newly-excluded files). Without this, an agent
	// writing a .docgraphignore exclusion on a live server would see no effect
	// until some other file happened to change.
	if !isIgnoreRuleFile && !docformat.SupportedExt(strings.ToLower(filepath.Ext(name))) {
		return "", "", false
	}
	root = findProjectRoot(name, roots)
	if root == "" {
		return "", "", false
	}
	rel, err := filepath.Rel(root, name)
	if err != nil {
		return "", "", false
	}
	return root, rel, true
}

// debouncer coalesces rapid filesystem events into batched onChange calls. The
// per-call state that was previously captured as local closure variables now
// lives here so it can be reasoned about separately.
//
// Concurrency contract: schedule is called ONLY from the single event-loop
// goroutine, so d.timer is touched by exactly one goroutine and requires no
// locking. The mutex guards ONLY d.pending, which is also written by the
// AfterFunc timer goroutine. The AfterFunc callback MUST NOT touch d.timer.
type debouncer struct {
	mu       sync.Mutex
	pending  map[string][]string
	timer    *time.Timer
	debounce time.Duration
	onChange OnChangeFunc
}

// schedule appends rel to the pending set for root and arms (or re-arms) the
// debounce timer. It preserves the original Stop-then-reassign idiom exactly:
// timer is stopped before reassignment so the previous AfterFunc callback cannot
// fire while the new one is being installed. The callback swaps d.pending under
// the mutex, releases the lock, then dispatches each project batch through a
// per-project recover wrapper so a panicking reindex cannot kill the server.
func (d *debouncer) schedule(root, rel string) {
	d.mu.Lock()
	d.pending[root] = append(d.pending[root], rel)
	d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.debounce, func() {
		d.mu.Lock()
		batch := d.pending
		d.pending = make(map[string][]string)
		d.mu.Unlock()
		for projectPath, files := range batch {
			// onChange runs the reindex pipeline (parse → resolver →
			// similarity → store) over files that just changed on disk —
			// i.e. untrusted content. This callback executes on the
			// AfterFunc timer goroutine, which has no parent recover, so
			// an unrecovered panic here (e.g. a malformed document that
			// slips past the per-file parser recover) would kill the
			// whole serve process. Contain it per project so one bad
			// file can neither crash the server nor block re-indexing of
			// the other projects in the batch.
			func(projectPath string, files []string) {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("watcher: recovered from panic re-indexing %s: %v", projectPath, r)
					}
				}()
				d.onChange(projectPath, files)
			}(projectPath, files)
		}
	})
}

// handleCreateDir watches a newly-created directory. It is called BEFORE
// classifyWatchEvent in the event loop to preserve the ordering invariant: new
// directories must be registered with fsnotify even when they contain no
// supported-ext files (which would cause classify to return ok=false).
func handleCreateDir(w *fsnotify.Watcher, event fsnotify.Event, budget *watchBudget) {
	if event.Op&fsnotify.Create == 0 {
		return
	}
	if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
		_ = addRecursive(w, event.Name, budget)
	}
}

func WatchWithContext(ctx context.Context, paths []string, debounce time.Duration, maxWatches int, onChange OnChangeFunc) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	budget := &watchBudget{max: maxWatches}

	roots, err := addRoots(w, paths)
	if err != nil {
		return err
	}
	go func() {
		for _, abs := range roots {
			_ = addRecursive(w, abs, budget)
		}
	}()

	deb := &debouncer{
		pending:  make(map[string][]string),
		debounce: debounce,
		onChange: onChange,
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-w.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove) == 0 {
				continue
			}
			handleCreateDir(w, event, budget)
			root, rel, ok := classifyWatchEvent(event.Name, roots)
			if !ok {
				continue
			}
			deb.schedule(root, rel)
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			log.Printf("watcher error: %v", err)
		}
	}
}

func findProjectRoot(filePath string, roots []string) string {
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return ""
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, absFile)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return root
		}
	}
	return ""
}

func addRecursive(w *fsnotify.Watcher, root string, b *watchBudget) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Bound fd usage: on kqueue every watched directory and file is one
			// open fd, so stop descending once the budget is spent rather than
			// march the whole machine into file-table exhaustion. SkipDir leaves
			// this directory and its subtree unwatched (its parent's watch already
			// spent one fd discovering it); the stale tail re-syncs on the next
			// `docgraph sync`/restart.
			if b.full() {
				b.warnOnce()
				return filepath.SkipDir
			}
			if err := w.Add(path); err != nil {
				return err
			}
			b.inc()
			return nil
		}
		// A regular file: fsnotify auto-watches every entry of a watched directory,
		// so this file already holds one fd. Count it so the budget tracks true fd
		// cost (files dominate the tree), not just directory count.
		b.inc()
		return nil
	})
}

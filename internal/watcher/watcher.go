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

func WatchWithContext(ctx context.Context, paths []string, debounce time.Duration, maxWatches int, onChange OnChangeFunc) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	budget := &watchBudget{max: maxWatches}

	roots := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("abs path %s: %w", p, err)
		}
		// Add only the root synchronously so the event loop can start immediately.
		// Subdirectory expansion runs in the background; new dirs created after
		// startup are handled by the Create-event handler below. The root's own fd
		// is counted by the addRecursive walk below (its first visit re-Adds the
		// root idempotently), so it is not double-counted here.
		if err := w.Add(abs); err != nil {
			return fmt.Errorf("watch %s: %w", abs, err)
		}
		roots = append(roots, abs)
	}
	go func() {
		for _, abs := range roots {
			_ = addRecursive(w, abs, budget)
		}
	}()

	var (
		mu      sync.Mutex
		pending = make(map[string][]string)
		timer   *time.Timer
	)

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
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					_ = addRecursive(w, event.Name, budget)
				}
			}
			if !docformat.SupportedExt(strings.ToLower(filepath.Ext(event.Name))) {
				continue
			}
			root := findProjectRoot(event.Name, roots)
			if root == "" {
				continue
			}
			rel, err := filepath.Rel(root, event.Name)
			if err != nil {
				continue
			}
			mu.Lock()
			pending[root] = append(pending[root], rel)
			mu.Unlock()
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounce, func() {
				mu.Lock()
				batch := pending
				pending = make(map[string][]string)
				mu.Unlock()
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
						onChange(projectPath, files)
					}(projectPath, files)
				}
			})
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

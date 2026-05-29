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

func Watch(paths []string, onChange OnChangeFunc) error {
	return WatchWithContext(context.Background(), paths, 2*time.Second, onChange)
}

func WatchWithContext(ctx context.Context, paths []string, debounce time.Duration, onChange OnChangeFunc) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	roots := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("abs path %s: %w", p, err)
		}
		// Add only the root synchronously so the event loop can start immediately.
		// Subdirectory expansion runs in the background; new dirs created after
		// startup are handled by the Create-event handler below.
		if err := w.Add(abs); err != nil {
			return fmt.Errorf("watch %s: %w", abs, err)
		}
		roots = append(roots, abs)
	}
	go func() {
		for _, abs := range roots {
			_ = addRecursive(w, abs)
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
					_ = addRecursive(w, event.Name)
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

func addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return w.Add(path)
		}
		return nil
	})
}

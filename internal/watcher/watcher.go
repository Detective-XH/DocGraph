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
		if err := addRecursive(w, abs); err != nil {
			return fmt.Errorf("watch %s: %w", abs, err)
		}
		roots = append(roots, abs)
	}

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
					onChange(projectPath, files)
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

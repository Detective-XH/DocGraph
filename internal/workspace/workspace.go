package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Detective-XH/docgraph/internal/store"
)

type Project struct {
	Name                string
	Path                string
	Store               *store.Store
	NoGitignore         bool
	NoHistory           bool
	SimilarityThreshold float64
}
type Workspace struct {
	Root                string
	Projects            []*Project
	NoGitignore         bool
	NoHistory           bool
	SimilarityThreshold float64
}

func Open(root string) (*Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	excluded := loadExcludeList(filepath.Join(abs, ".docgraphignore"))
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}

	type candidate struct{ name, dir string }
	var candidates []candidate
	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		if excluded[e.Name()] {
			fmt.Fprintf(os.Stderr, "[workspace] excluding %s (.docgraphignore)\n", e.Name())
			continue
		}
		dir := filepath.Join(abs, e.Name())
		if err := os.MkdirAll(filepath.Join(dir, ".docgraph"), 0o755); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate{e.Name(), dir})
	}

	// Open all project stores in parallel; bootstrapSchema + SyncDomainPacks dominate startup time.
	type openResult struct {
		proj *Project
		err  error
	}
	results := make([]openResult, len(candidates))
	var wg sync.WaitGroup
	for i, c := range candidates {
		wg.Add(1)
		go func(i int, c candidate) {
			defer wg.Done()
			st, err := store.Open(filepath.Join(c.dir, ".docgraph", "docgraph.db"))
			results[i] = openResult{&Project{Name: c.name, Path: c.dir, Store: st}, err}
		}(i, c)
	}
	wg.Wait()

	w := &Workspace{Root: abs}
	for _, r := range results {
		if r.err != nil {
			for _, r2 := range results {
				if r2.proj != nil && r2.proj.Store != nil {
					r2.proj.Store.Close()
				}
			}
			return nil, r.err
		}
		w.Projects = append(w.Projects, r.proj)
	}
	sort.Slice(w.Projects, func(i, j int) bool { return w.Projects[i].Name < w.Projects[j].Name })
	return w, nil
}
func (w *Workspace) Close() error {
	var last error
	for _, p := range w.Projects {
		if err := p.Store.Close(); err != nil {
			last = err
		}
	}
	return last
}
func (w *Workspace) Search(query, kind string, limit int) ([]store.SearchResult, error) {
	return w.SearchWithOptions(store.SearchOptions{Query: query, Kind: kind, Limit: limit})
}
func (w *Workspace) SearchWithOptions(opts store.SearchOptions) ([]store.SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	var all []store.SearchResult
	for _, p := range w.Projects {
		if opts.ProjectFilter != "" && p.Name != opts.ProjectFilter {
			continue
		}
		perProjectCap := limit * 2
		projectOpts := opts
		projectOpts.Limit = perProjectCap
		results, err := p.Store.Searcher.SearchWithOptions(projectOpts)
		if err != nil {
			continue
		}
		for i := range results {
			annotateNode(p, &results[i].Node)
		}
		all = append(all, results...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Rank < all[j].Rank })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
func (w *Workspace) GetAllStats() (map[string]store.Stats, error) {
	m := make(map[string]store.Stats)
	for _, p := range w.Projects {
		if s, err := p.Store.GetStats(); err == nil {
			m[p.Name] = s
		}
	}
	return m, nil
}
func (w *Workspace) GetAllFiles(pathFilter, projectFilter string) (map[string][]store.FileInfo, error) {
	m := make(map[string][]store.FileInfo)
	for _, p := range w.Projects {
		if projectFilter != "" && p.Name != projectFilter {
			continue
		}
		if files, err := p.Store.GetFiles(pathFilter); err == nil {
			m[p.Name] = files
		}
	}
	return m, nil
}

// GetAllTopLevelDirs fans out GetTopLevelDirs across all projects, deduplicates
// the segments, and returns them sorted. Per-project errors are silently ignored,
// mirroring GetAllFiles' error-handling policy.
func (w *Workspace) GetAllTopLevelDirs(projectFilter string) ([]string, error) {
	seen := make(map[string]struct{})
	for _, p := range w.Projects {
		if projectFilter != "" && p.Name != projectFilter {
			continue
		}
		dirs, err := p.Store.GetTopLevelDirs()
		if err != nil {
			continue
		}
		for _, d := range dirs {
			seen[d] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for d := range seen {
		result = append(result, d)
	}
	sort.Strings(result)
	return result, nil
}
func (w *Workspace) FindProject(name string) *Project {
	for _, p := range w.Projects {
		if p.Name == name {
			return p
		}
	}
	return nil
}
func (w *Workspace) FindNodeByName(name string) (*store.Node, string, error) {
	for _, p := range w.Projects {
		if n, err := p.Store.FindNodeByName(name); err == nil && n != nil {
			annotateNode(p, n)
			return n, p.Name, nil
		}
	}
	return nil, "", nil
}
func (w *Workspace) FindNodeByPath(path string) (*store.Node, string, error) {
	for _, p := range w.Projects {
		if n, err := p.Store.FindNodeByPath(path); err == nil && n != nil {
			annotateNode(p, n)
			return n, p.Name, nil
		}
	}
	return nil, "", nil
}

func annotateNode(p *Project, n *store.Node) {
	if p == nil || n == nil {
		return
	}
	n.ProjectName = p.Name
	if n.QualifiedName != "" && !strings.HasPrefix(n.QualifiedName, "[") {
		n.QualifiedName = "[" + p.Name + "] " + n.QualifiedName
	}
}

func loadExcludeList(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	m := make(map[string]bool)
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line[0] != '#' {
			m[line] = true
		}
	}
	return m
}

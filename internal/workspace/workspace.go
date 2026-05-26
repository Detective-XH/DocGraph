package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Detective-XH/docgraph/internal/codedoc"
	"github.com/Detective-XH/docgraph/internal/extractor"
	"github.com/Detective-XH/docgraph/internal/git"
	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/resolver"
	"github.com/Detective-XH/docgraph/internal/scanner"
	"github.com/Detective-XH/docgraph/internal/similarity"
	"github.com/Detective-XH/docgraph/internal/store"
)

type Project struct {
	Name                string
	Path                string
	Store               *store.Store
	NoGitignore         bool
	SimilarityThreshold float64
}
type Workspace struct {
	Root                string
	Projects            []*Project
	NoGitignore         bool
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
	w := &Workspace{Root: abs}
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
		st, err := store.Open(filepath.Join(dir, ".docgraph", "docgraph.db"))
		if err != nil {
			return nil, err
		}
		w.Projects = append(w.Projects, &Project{Name: e.Name(), Path: dir, Store: st})
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
func (w *Workspace) IndexAll() error {
	for _, p := range w.Projects {
		p.NoGitignore = w.NoGitignore
		p.SimilarityThreshold = w.SimilarityThreshold
		if err := indexProjectOpts(p, w.NoGitignore, w.SimilarityThreshold); err != nil {
			fmt.Fprintf(os.Stderr, "index %s: %v\n", p.Name, err)
		}
	}

	// Second-pass: resolve cross-project [[project/doc-name]] wikilinks
	crossRefs := make([]resolver.ProjectRef, 0, len(w.Projects))
	for _, p := range w.Projects {
		crossRefs = append(crossRefs, resolver.ProjectRef{Name: p.Name, Store: p.Store})
	}
	if err := resolver.ResolveWorkspace(crossRefs); err != nil {
		fmt.Fprintf(os.Stderr, "[workspace] cross-project resolve: %v\n", err)
	}

	return nil
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
		perProjectCap := limit * 2
		projectOpts := opts
		projectOpts.Limit = perProjectCap
		results, err := p.Store.SearchWithOptions(projectOpts)
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
func (w *Workspace) GetAllFiles(pathFilter string) (map[string][]store.FileInfo, error) {
	m := make(map[string][]store.FileInfo)
	for _, p := range w.Projects {
		if files, err := p.Store.GetFiles(pathFilter); err == nil {
			m[p.Name] = files
		}
	}
	return m, nil
}
func (w *Workspace) FindProject(name string) *Project {
	for _, p := range w.Projects {
		if p.Name == name {
			return p
		}
	}
	return nil
}
func (w *Workspace) GetIncomingEdges(projectName, nodeID string) ([]store.Edge, error) {
	if p := w.FindProject(projectName); p != nil {
		return p.Store.GetIncomingEdges(nodeID)
	}
	return nil, fmt.Errorf("project %q not found", projectName)
}
func (w *Workspace) GetOutgoingEdges(projectName, nodeID string) ([]store.Edge, error) {
	if p := w.FindProject(projectName); p != nil {
		return p.Store.GetOutgoingEdges(nodeID)
	}
	return nil, fmt.Errorf("project %q not found", projectName)
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
func ReindexProject(p *Project) {
	if err := indexProject(p); err != nil {
		fmt.Fprintf(os.Stderr, "[reindex] %s: %v\n", p.Name, err)
	}
}

func indexProjectNoGitignore(p *Project, noGitignore bool) error {
	return indexProjectOpts(p, noGitignore, p.SimilarityThreshold)
}

func indexProject(p *Project) error {
	return indexProjectOpts(p, p.NoGitignore, p.SimilarityThreshold)
}

func indexProjectOpts(p *Project, noGitignore bool, threshold float64) error {
	entries, err := scanner.ScanDirOpts(p.Path, scanner.ScanOptions{NoGitignore: noGitignore})
	if err != nil {
		return err
	}
	codeDocEnabled, err := p.Store.IsPackEnabled("code_doc")
	if err != nil {
		return fmt.Errorf("[%s] code_doc pack state: %w", p.Name, err)
	}
	var nNew, nSkip int
	var changedDocIDs []string
	for _, e := range entries {
		if !codeDocEnabled && codedoc.IsCodeExt(strings.ToLower(filepath.Ext(e.RelPath))) {
			continue
		}
		src, err := os.ReadFile(e.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", e.RelPath, err)
			continue
		}
		h := sha256.Sum256(src)
		hash := hex.EncodeToString(h[:])
		if old, _ := p.Store.GetFileHash(e.RelPath); hash == old {
			nSkip++
			continue
		}
		// Delete derived rows before DeleteFileData so cascade-deleted node IDs are still reachable.
		p.Store.DeleteSectionChunksByFile(e.RelPath)
		p.Store.DeleteDocumentMetadataByFile(e.RelPath)
		p.Store.DeleteFileData(e.RelPath)
		res, err := parseIndexedFile(e.Path, e.RelPath, src, hash)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", e.RelPath, err)
			continue
		}
		nodes := append([]store.Node{res.DocNode}, res.Headings...)
		nodes = append(nodes, res.Defs...)
		nodes = append(nodes, res.Tags...)
		res.FileInfo.ModifiedAt = e.ModifiedAt
		if err := p.Store.InsertNodes(nodes); err != nil {
			return err
		}
		if len(res.SectionChunks) > 0 {
			if err := p.Store.UpsertSectionChunks(res.SectionChunks); err != nil {
				return err
			}
		}
		if len(res.MetadataTuples) > 0 {
			if err := p.Store.InsertDocumentMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
				return fmt.Errorf("[%s] metadata %s: %w", p.Name, e.RelPath, err)
			} else if err := p.Store.UpsertGovernanceMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
				return fmt.Errorf("[%s] governance %s: %w", p.Name, e.RelPath, err)
			} else if err := p.Store.UpsertResearchMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
				return fmt.Errorf("[%s] research %s: %w", p.Name, e.RelPath, err)
			}
		}
		if err := p.Store.InsertEdges(res.Edges); err != nil {
			return err
		}
		if len(res.RawLinks) > 0 {
			refs := make([]store.UnresolvedRef, 0, len(res.RawLinks))
			for _, rl := range res.RawLinks {
				refs = append(refs, store.UnresolvedRef{
					FromNodeID: rl.FromNodeID, ReferenceText: rl.Target,
					ReferenceKind: rl.Kind, Line: rl.Line, FilePath: e.RelPath})
			}
			if err := p.Store.InsertUnresolvedRefs(refs); err != nil {
				return err
			}
		}
		if err := p.Store.UpsertFile(res.FileInfo); err != nil {
			return err
		}
		fh := git.CollectHistory(p.Path, e.RelPath)
		if err := p.Store.UpsertFileHistory(store.FileHistory{
			Path:          fh.Path,
			CommitCount:   fh.CommitCount,
			FirstCommitAt: fh.FirstCommitAt,
			LastCommitAt:  fh.LastCommitAt,
			AuthorCount:   fh.AuthorCount,
			LastAuthor:    fh.LastAuthor,
			LastSubject:   fh.LastSubject,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] history %s: %v\n", p.Name, e.RelPath, err)
		}
		nNew++
		changedDocIDs = append(changedDocIDs, res.DocNode.ID)
	}
	fmt.Fprintf(os.Stderr, "[%s] Indexed %d files (%d new, %d unchanged)\n", p.Name, len(entries), nNew, nSkip)
	if nNew > 0 {
		if err := resolver.Resolve(p.Store); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] resolver: %v\n", p.Name, err)
		}
		if err := similarity.ComputeSimilarityIncremental(p.Store, changedDocIDs, threshold); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] similarity: %v\n", p.Name, err)
		}

	}
	return nil
}

func parseIndexedFile(absPath, relPath string, src []byte, hash string) (*parser.ParseResult, error) {
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == ".md" {
		return parser.ParseFile(absPath, relPath, src, hash)
	}
	if codedoc.IsCodeExt(ext) {
		return codedoc.Extract(absPath, relPath, src, hash)
	}
	return extractor.Extract(absPath, relPath, src, hash)
}

func loadExcludeList(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	m := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line[0] != '#' {
			m[line] = true
		}
	}
	return m
}

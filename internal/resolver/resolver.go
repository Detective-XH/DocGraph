package resolver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
)

// ProjectRef is a lightweight project descriptor used by ResolveWorkspace
// to avoid a circular import between resolver and workspace packages.
type ProjectRef struct {
	Name  string
	Store *store.Store
}

// ResolveWorkspace performs a second-pass cross-project wikilink resolution
// after all per-project Resolve() calls have completed.
// It handles [[project/doc-name]] formatted wikilinks that could not be
// resolved within a single project.
// Because edges.target has a FOREIGN KEY constraint that references nodes in
// the same DB, cross-project edges use the same self-edge-with-metadata
// pattern as links_external: Source == Target == ref.FromNodeID, with
// {"cross_project": true, "target_project": "...", "target_node_id": "..."}
// stored in Metadata.
func ResolveWorkspace(projects []ProjectRef) error {
	// Build global lookup: byProject[projectName][basename] = nodeID
	type byBasenameMap = map[string]string
	byProject := make(map[string]byBasenameMap, len(projects))

	for _, p := range projects {
		docs, err := p.Store.GetAllDocumentNodes()
		if err != nil {
			return fmt.Errorf("load docs for project %q: %w", p.Name, err)
		}
		m := make(byBasenameMap, len(docs))
		for _, d := range docs {
			base := strings.ToLower(strings.TrimSuffix(filepath.Base(d.FilePath), ".md"))
			// Last writer wins on collision — same disambiguation heuristic as fuzzyResolve
			if _, exists := m[base]; !exists {
				m[base] = d.ID
			}
		}
		byProject[p.Name] = m
	}

	var totalResolved int

	for _, p := range projects {
		refs, err := p.Store.GetUnresolvedRefs()
		if err != nil {
			return fmt.Errorf("get unresolved refs for project %q: %w", p.Name, err)
		}

		var edges []store.Edge
		var stillUnresolved []store.UnresolvedRef

		for _, ref := range refs {
			// Only handle cross-project wikilinks (contain a slash after pipe/anchor stripping)
			if ref.ReferenceKind != "wikilink" {
				stillUnresolved = append(stillUnresolved, ref)
				continue
			}

			target := ref.ReferenceText
			// Strip pipe alias and anchor
			if idx := strings.Index(target, "|"); idx >= 0 {
				target = target[:idx]
			}
			if idx := strings.Index(target, "#"); idx >= 0 {
				target = target[:idx]
			}
			target = strings.TrimSpace(target)

			slashIdx := strings.Index(target, "/")
			if slashIdx < 0 || slashIdx == 0 || slashIdx == len(target)-1 {
				// Not a cross-project reference — leave for intra-project pass
				stillUnresolved = append(stillUnresolved, ref)
				continue
			}

			parts := strings.SplitN(target, "/", 2)
			targetProject := parts[0]
			docName := strings.ToLower(strings.TrimSuffix(parts[1], ".md"))

			projectDocs, ok := byProject[targetProject]
			if !ok {
				// Referenced project doesn't exist
				stillUnresolved = append(stillUnresolved, ref)
				continue
			}

			targetNodeID, ok := projectDocs[docName]
			if !ok {
				// Document not found in target project
				stillUnresolved = append(stillUnresolved, ref)
				continue
			}

			meta, _ := json.Marshal(map[string]string{
				"cross_project":  "true",
				"target_project": targetProject,
				"target_node_id": targetNodeID,
			})
			edges = append(edges, store.Edge{
				Source:   ref.FromNodeID,
				Target:   ref.FromNodeID,
				Kind:     "wikilinks_to",
				Metadata: string(meta),
				Line:     ref.Line,
			})
		}

		if len(edges) > 0 {
			if err := p.Store.InsertEdges(edges); err != nil {
				return fmt.Errorf("insert cross-project edges for project %q: %w", p.Name, err)
			}
		}

		resolved := len(refs) - len(stillUnresolved)
		totalResolved += resolved

		if err := p.Store.DeleteAllUnresolvedRefs(); err != nil {
			return fmt.Errorf("delete unresolved refs for project %q: %w", p.Name, err)
		}
		if len(stillUnresolved) > 0 {
			if err := p.Store.InsertUnresolvedRefs(stillUnresolved); err != nil {
				return fmt.Errorf("re-insert unresolved refs for project %q: %w", p.Name, err)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "[workspace] cross-project resolve: %d cross-project wikilinks resolved\n", totalResolved)
	return nil
}

func Resolve(st RefResolver) error {
	docs, err := st.GetAllDocumentNodes()
	if err != nil {
		return fmt.Errorf("load document nodes: %w", err)
	}

	byPath := make(map[string]string)
	byBasename := make(map[string][]string)
	byName := make(map[string][]string)

	for _, d := range docs {
		lowerPath := strings.ToLower(d.FilePath)
		byPath[lowerPath] = d.ID

		base := strings.ToLower(strings.TrimSuffix(filepath.Base(d.FilePath), ".md"))
		byBasename[base] = append(byBasename[base], d.ID)

		name := strings.ToLower(d.Name)
		byName[name] = append(byName[name], d.ID)
	}

	nodeDir := make(map[string]string)
	for _, d := range docs {
		nodeDir[d.ID] = filepath.Dir(d.FilePath)
	}

	refs, err := st.GetUnresolvedRefs()
	if err != nil {
		return fmt.Errorf("load unresolved refs: %w", err)
	}

	var edges []store.Edge
	var stillUnresolved []store.UnresolvedRef

	for _, ref := range refs {
		target := strings.TrimSpace(ref.ReferenceText)
		if target == "" {
			stillUnresolved = append(stillUnresolved, ref)
			continue
		}

		switch ref.ReferenceKind {
		case "markdown_link":
			edge := resolveMarkdownLink(ref, target, byPath, st)
			if edge != nil {
				edges = append(edges, *edge)
			} else {
				stillUnresolved = append(stillUnresolved, ref)
			}

		case "wikilink":
			edge := resolveWikilink(ref, target, byBasename, byName, nodeDir)
			if edge != nil {
				edges = append(edges, *edge)
			} else {
				stillUnresolved = append(stillUnresolved, ref)
			}

		case "embed":
			edge := resolveEmbed(ref, target, byBasename, byName, nodeDir)
			if edge != nil {
				edges = append(edges, *edge)
			} else {
				stillUnresolved = append(stillUnresolved, ref)
			}

		case "external":
			meta, _ := json.Marshal(map[string]string{"url": target})
			edges = append(edges, store.Edge{
				Source:   ref.FromNodeID,
				Target:   ref.FromNodeID,
				Kind:     "links_external",
				Metadata: string(meta),
				Line:     ref.Line,
			})

		default:
			stillUnresolved = append(stillUnresolved, ref)
		}
	}

	if len(edges) > 0 {
		if err := st.InsertEdges(edges); err != nil {
			return fmt.Errorf("insert edges: %w", err)
		}
	}

	if err := st.DeleteAllUnresolvedRefs(); err != nil {
		return fmt.Errorf("delete unresolved refs: %w", err)
	}

	if len(stillUnresolved) > 0 {
		if err := st.InsertUnresolvedRefs(stillUnresolved); err != nil {
			return fmt.Errorf("re-insert unresolved refs: %w", err)
		}
	}

	resolved := len(refs) - len(stillUnresolved)
	fmt.Fprintf(os.Stderr, "Resolved %d references (%d unresolved remaining)\n", resolved, len(stillUnresolved))
	return nil
}

func resolveMarkdownLink(ref store.UnresolvedRef, target string, byPath map[string]string, st RefResolver) *store.Edge {
	anchor := ""
	if idx := strings.Index(target, "#"); idx >= 0 {
		anchor = target[idx+1:]
		target = target[:idx]
	}

	if target == "" {
		if anchor == "" {
			return nil
		}
		target = ref.FilePath
	}

	resolved := filepath.Join(filepath.Dir(ref.FilePath), target)
	resolved = filepath.Clean(resolved)

	nodeID, ok := byPath[strings.ToLower(resolved)]
	if !ok {
		return nil
	}

	finalTarget := nodeID
	if anchor != "" {
		headingID := resolved + "#" + anchor
		if n, err := st.GetNodeByID(headingID); err == nil && n != nil {
			finalTarget = headingID
		}
	}

	return &store.Edge{
		Source: ref.FromNodeID,
		Target: finalTarget,
		Kind:   "references",
		Line:   ref.Line,
	}
}

func resolveWikilink(ref store.UnresolvedRef, target string, byBasename, byName map[string][]string, nodeDir map[string]string) *store.Edge {
	if idx := strings.Index(target, "|"); idx >= 0 {
		target = target[:idx]
	}

	if idx := strings.Index(target, "#"); idx >= 0 {
		target = target[:idx]
	}

	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}

	nodeID := fuzzyResolve(target, ref.FilePath, byBasename, byName, nodeDir)
	if nodeID == "" {
		return nil
	}

	return &store.Edge{
		Source: ref.FromNodeID,
		Target: nodeID,
		Kind:   "wikilinks_to",
		Line:   ref.Line,
	}
}

func resolveEmbed(ref store.UnresolvedRef, target string, byBasename, byName map[string][]string, nodeDir map[string]string) *store.Edge {
	if idx := strings.Index(target, "|"); idx >= 0 {
		target = target[:idx]
	}

	if idx := strings.Index(target, "#"); idx >= 0 {
		target = target[:idx]
	}

	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(target))
	if ext != "" && ext != ".md" {
		return nil
	}

	nodeID := fuzzyResolve(target, ref.FilePath, byBasename, byName, nodeDir)
	if nodeID == "" {
		return nil
	}

	return &store.Edge{
		Source: ref.FromNodeID,
		Target: nodeID,
		Kind:   "embeds",
		Line:   ref.Line,
	}
}

func fuzzyResolve(target, sourceFilePath string, byBasename, byName map[string][]string, nodeDir map[string]string) string {
	key := strings.ToLower(strings.TrimSuffix(target, ".md"))

	if ids, ok := byBasename[key]; ok {
		return disambiguate(ids, sourceFilePath, nodeDir)
	}

	if ids, ok := byName[key]; ok {
		return disambiguate(ids, sourceFilePath, nodeDir)
	}

	return ""
}

func disambiguate(ids []string, sourceFilePath string, nodeDir map[string]string) string {
	if len(ids) == 1 {
		return ids[0]
	}

	srcDir := filepath.Dir(sourceFilePath)

	var sameDir []string
	for _, id := range ids {
		if nodeDir[id] == srcDir {
			sameDir = append(sameDir, id)
		}
	}
	if len(sameDir) == 1 {
		return sameDir[0]
	}
	if len(sameDir) > 1 {
		ids = sameDir
	}

	shortest := ids[0]
	for _, id := range ids[1:] {
		if len(id) < len(shortest) {
			shortest = id
		}
	}
	return shortest
}

package tools

import (
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
)

func (h *handler) getStoreForNode(nodeID string) *store.Store {
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			if n, err := p.Store.GetNodeByID(nodeID); err == nil && n != nil {
				return p.Store
			}
		}
		return nil
	}
	return h.store
}

func (h *handler) getStoreForResolvedNode(node *store.Node) *store.Store {
	if node == nil {
		return nil
	}
	if h.workspace != nil && node.ProjectName != "" {
		if p := h.workspace.FindProject(node.ProjectName); p != nil {
			return p.Store
		}
	}
	return h.getStoreForNode(node.ID)
}

func (h *handler) getProjectRootForResolvedNode(node *store.Node) string {
	if node == nil {
		return ""
	}
	if h.workspace != nil && node.ProjectName != "" {
		if p := h.workspace.FindProject(node.ProjectName); p != nil {
			return p.Path
		}
	}
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			if n, err := p.Store.GetNodeByID(node.ID); err == nil && n != nil {
				return p.Path
			}
		}
		return ""
	}
	return h.projectRoot
}

func (h *handler) getHeadings(node *store.Node) []store.Node {
	if h.workspace != nil {
		if node.ProjectName != "" {
			if p := h.workspace.FindProject(node.ProjectName); p != nil {
				hs, _ := p.Store.GetChildHeadings(node.FilePath)
				for i := range hs {
					hs[i].ProjectName = p.Name
					if hs[i].QualifiedName != "" && !strings.HasPrefix(hs[i].QualifiedName, "[") {
						hs[i].QualifiedName = "[" + p.Name + "] " + hs[i].QualifiedName
					}
				}
				return hs
			}
		}
		for _, p := range h.workspace.Projects {
			if hs, err := p.Store.GetChildHeadings(node.FilePath); err == nil && len(hs) > 0 {
				for i := range hs {
					hs[i].ProjectName = p.Name
				}
				return hs
			}
		}
		return nil
	}
	hs, _ := h.store.GetChildHeadings(node.FilePath)
	return hs
}

func (h *handler) getEdgeCounts(node *store.Node) (inCount, outCount int) {
	if h.workspace != nil {
		if st := h.getStoreForResolvedNode(node); st != nil {
			if es, err := st.GetIncomingEdges(node.ID); err == nil {
				inCount = len(es)
			}
			if es, err := st.GetOutgoingEdges(node.ID); err == nil {
				outCount = len(es)
			}
			return
		}
		for _, p := range h.workspace.Projects {
			if es, err := p.Store.GetIncomingEdges(node.ID); err == nil {
				inCount += len(es)
			}
			if es, err := p.Store.GetOutgoingEdges(node.ID); err == nil {
				outCount += len(es)
			}
		}
	} else {
		if es, err := h.store.GetIncomingEdges(node.ID); err == nil {
			inCount = len(es)
		}
		if es, err := h.store.GetOutgoingEdges(node.ID); err == nil {
			outCount = len(es)
		}
	}
	return
}

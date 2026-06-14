package resolver

import "github.com/Detective-XH/docgraph/internal/store"

// RefResolver is the narrow slice of *store.Store that the reference
// resolver actually needs. It decouples resolver from the 125-method
// *store.Store god type: callers pass a concrete *store.Store (which
// auto-satisfies this subset), but the resolver only depends on these
// six methods. This shrinks the blast radius of store API changes on
// the resolver and documents the true dependency surface.
type RefResolver interface {
	GetAllDocumentNodes() ([]store.Node, error)
	GetUnresolvedRefs() ([]store.UnresolvedRef, error)
	InsertEdges(edges []store.Edge) error
	InsertUnresolvedRefs(refs []store.UnresolvedRef) error
	DeleteAllUnresolvedRefs() error
	GetNodeByID(id string) (*store.Node, error)
}

// Compile-time assertion that the concrete store satisfies the narrow
// interface, so the auto-satisfaction relied on at call sites can never
// silently break.
var _ RefResolver = (*store.Store)(nil)

package similarity

import "github.com/Detective-XH/docgraph/internal/store"

// SimilarityStore is the narrow slice of *store.Store that the similarity engine
// depends on. Decoupling from the 125-method god type keeps this package's
// contract explicit and testable: callers still pass a concrete *store.Store
// (which satisfies this interface), so no call site changes. The methods cover
// document/edge reads (full and incremental rebuilds), similar_to edge cleanup
// and insertion (TF-IDF and neural paths), neural embedding reads, and the
// project-metadata threshold cache.
type SimilarityStore interface {
	GetAllDocumentNodes() ([]store.Node, error)
	GetEdgesBySource(sourceID string) ([]store.Edge, error)
	DeleteEdgesByKind(kind string) error
	DeleteSimilarityEdgesForDocs(nodeIDs []string) error
	DeleteNeuralSimilarityEdgesForDoc(docID string) error
	InsertEdges(edges []store.Edge) error
	GetEmbeddingsByModel(modelID string) ([]store.Embedding, error)
	GetProjectMeta(key string) (string, bool, error)
	UpsertProjectMeta(key, value string) error
}

// Compile-time assertion that the concrete store still satisfies the interface.
var _ SimilarityStore = (*store.Store)(nil)

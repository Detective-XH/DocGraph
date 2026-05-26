// Package entitygraph implements F-29 entity and source graph runtime.
// Extraction primitives and dedup logic; Entity/Mention types live in
// internal/store to avoid a circular import (entitygraph → parser → store).
package entitygraph

import "github.com/Detective-XH/docgraph/internal/store"

// ExtractResult is the output of extraction from a single ParseResult.
type ExtractResult struct {
	Entities []store.Entity
	Mentions []store.Mention
}

const (
	MaxAliases    = 200
	MaxContextLen = 500
	MaxEntities   = 500
)

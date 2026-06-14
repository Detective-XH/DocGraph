package store

// Thin forwarders on *Store that expose embedded/sub-store behaviour as methods.
//
// Go interfaces cannot expose struct fields, so consumer packages that want to
// depend on a narrow interface (rather than the concrete *Store) need method
// access to two pieces of state: the IndexMu mutex embedded via *baseDB, and the
// entity sub-store reached through Store.Entity. These wrappers let a consumer
// declare exactly the surface it uses (e.g. an "entity writer" or "index locker"
// interface) and accept *Store transparently. Purely additive — each method just
// delegates; no new behaviour lives here.

// LockIndex acquires the index mutex, serialising index/reindex on this store.
func (s *Store) LockIndex() { s.IndexMu.Lock() }

// UnlockIndex releases the index mutex acquired by LockIndex.
func (s *Store) UnlockIndex() { s.IndexMu.Unlock() }

// DeleteEntityData removes all entity mentions for filePath, then prunes orphans.
func (s *Store) DeleteEntityData(filePath string) error {
	return s.Entity.DeleteEntityData(filePath)
}

// PruneOrphanEntities deletes entities with no remaining entity_mentions rows.
func (s *Store) PruneOrphanEntities() error {
	return s.Entity.PruneOrphanEntities()
}

// InsertEntities upserts entities by (entity_type, canonical_name_normalized).
func (s *Store) InsertEntities(entities []Entity) error {
	return s.Entity.InsertEntities(entities)
}

// InsertEntityMentions inserts mentions, ignoring duplicates on (entity_id, node_id, line).
func (s *Store) InsertEntityMentions(mentions []Mention) error {
	return s.Entity.InsertEntityMentions(mentions)
}

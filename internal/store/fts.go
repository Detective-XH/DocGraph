package store

import "fmt"

// sectionChunksFTSTriggersSQL is the canonical DDL for ALL THREE triggers that
// keep section_chunks_fts in sync with the section_chunks base table. It MUST stay
// byte-identical to the copies inlined in SchemaSQL (schema.go) —
// TestSectionFTSTriggersMatchSchema guards that.
//
// The bulk-rebuild fast path drops all three during a full build and recreates
// them from here afterwards. All three (not just _insert) must be dropped: a fresh
// build still fires _update via UpsertSectionChunks' ON CONFLICT(node_id) DO UPDATE
// when the corpus contains duplicate section node_ids (e.g. repeated heading IDs in
// one doc). With _insert dropped but _update live, that UPDATE issues a 'delete'
// against an FTS posting that was never inserted → "database disk image is
// malformed". Dropping all three guarantees no FTS trigger fires during the bulk
// load; the final 'rebuild' reconstructs the FTS from the settled base table.
const sectionChunksFTSTriggersSQL = `CREATE TRIGGER IF NOT EXISTS section_chunks_fts_insert AFTER INSERT ON section_chunks BEGIN
    INSERT INTO section_chunks_fts(rowid, heading_path, text)
    VALUES (NEW.rowid, NEW.heading_path, NEW.text);
END;
CREATE TRIGGER IF NOT EXISTS section_chunks_fts_update AFTER UPDATE ON section_chunks BEGIN
    INSERT INTO section_chunks_fts(section_chunks_fts, rowid, heading_path, text)
    VALUES ('delete', OLD.rowid, OLD.heading_path, OLD.text);
    INSERT INTO section_chunks_fts(rowid, heading_path, text)
    VALUES (NEW.rowid, NEW.heading_path, NEW.text);
END;
CREATE TRIGGER IF NOT EXISTS section_chunks_fts_delete AFTER DELETE ON section_chunks BEGIN
    INSERT INTO section_chunks_fts(section_chunks_fts, rowid, heading_path, text)
    VALUES ('delete', OLD.rowid, OLD.heading_path, OLD.text);
END;`

// SectionFTSIsEmpty reports whether the section_chunks_fts INDEX has no documents.
// It gates the bulk-rebuild fast path (and its crash self-heal): a from-scratch
// build OR a crash that left the FTS empty while base rows survive both need a
// rebuild. The probe queries the FTS5 shadow table section_chunks_fts_docsize
// (one row per indexed doc, detail=full), NOT `count(*) FROM section_chunks_fts`:
// for an external-content FTS the latter reflects the CONTENT table, so it stays
// non-zero after 'delete-all' / a crash and would miss the self-heal case.
func (s *Store) SectionFTSIsEmpty() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM section_chunks_fts_docsize`).Scan(&n); err != nil {
		return false, fmt.Errorf("SectionFTSIsEmpty: %w", err)
	}
	return n == 0, nil
}

// DropSectionFTSTriggers removes all three section_chunks_fts sync triggers so a
// bulk load writes section_chunks base rows (incl. ON CONFLICT updates and deletes)
// without touching the FTS index. Idempotent (DROP ... IF EXISTS).
func (s *Store) DropSectionFTSTriggers() error {
	for _, name := range []string{"section_chunks_fts_insert", "section_chunks_fts_update", "section_chunks_fts_delete"} {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS ` + name); err != nil {
			return fmt.Errorf("DropSectionFTSTriggers %s: %w", name, err)
		}
	}
	return nil
}

// CreateSectionFTSTriggers restores the three sync triggers after a bulk rebuild,
// so subsequent incremental indexing keeps section_chunks_fts current.
func (s *Store) CreateSectionFTSTriggers() error {
	if _, err := s.db.Exec(sectionChunksFTSTriggersSQL); err != nil {
		return fmt.Errorf("CreateSectionFTSTriggers: %w", err)
	}
	return nil
}

// DeleteAllSectionFTS empties section_chunks_fts without touching the base table
// (the external-content 'delete-all' command). Mainly a test seam for the
// crash-recovery state (base rows present, FTS empty); the production self-heal
// reaches the same state via a crash between bulk load and rebuild.
func (s *Store) DeleteAllSectionFTS() error {
	if _, err := s.db.Exec(`INSERT INTO section_chunks_fts(section_chunks_fts) VALUES('delete-all')`); err != nil {
		return fmt.Errorf("DeleteAllSectionFTS: %w", err)
	}
	return nil
}

// RebuildSectionFTS reconstructs section_chunks_fts from the section_chunks base
// table in one bulk pass (a single tokenize→sort→optimal-segment build, vs the
// repeated hash-flush+automerge of incremental trigger population — ~2.4x cheaper
// on a full build, measured). The caller MUST ensure section_chunks is fully
// populated AND the sync triggers are dropped, or rows would be double-indexed.
func (s *Store) RebuildSectionFTS() error {
	if _, err := s.db.Exec(`INSERT INTO section_chunks_fts(section_chunks_fts) VALUES('rebuild')`); err != nil {
		return fmt.Errorf("RebuildSectionFTS: %w", err)
	}
	return nil
}

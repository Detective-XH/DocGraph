package store

import (
	"encoding/json"
	"strings"
	"time"
)

// Entity is a canonical entity or source node.
// UUID PK is never changed on upsert so FK references in entity_mentions stay stable.
type Entity struct {
	ID                      string
	EntityType              string // pack vocab value or "" for generic
	CanonicalName           string
	CanonicalNameNormalized string            // lowercase trimmed; used for UNIQUE dedup
	Aliases                 []string          // JSON array, capped at MaxEntityAliases entries
	Properties              map[string]string // JSON object, pack-defined
	PackID                  string            // "" = generic
	UpdatedAt               int64
}

// Mention records one document/section reference to an entity.
type Mention struct {
	EntityID    string
	NodeID      string
	FilePath    string
	Line        int
	Context     string // ≤ MaxMentionContextLen chars
	MentionType string // "reference" | "definition" | "wikilink"
	UpdatedAt   int64
}

const (
	MaxEntityAliases     = 200
	MaxMentionContextLen = 500
	MaxEntitiesPerDoc    = 500
	maxAliasesJSON       = 10 * 1024 // 10 KB byte cap on serialised aliases
)

// InsertEntities upserts entities by (entity_type, canonical_name_normalized).
// PK is preserved on conflict; canonical ID is read back into entities[i].ID.
func (s *Store) InsertEntities(entities []Entity) error {
	if len(entities) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	upsert, err := tx.Prepare(`INSERT INTO entities (id,entity_type,canonical_name,canonical_name_normalized,aliases,properties,pack_id,updated_at) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(entity_type,canonical_name_normalized) DO UPDATE SET canonical_name=excluded.canonical_name,aliases=excluded.aliases,properties=excluded.properties,pack_id=excluded.pack_id,updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer upsert.Close()
	lookup, err := tx.Prepare(`SELECT id FROM entities WHERE entity_type=? AND canonical_name_normalized=?`)
	if err != nil {
		return err
	}
	defer lookup.Close()

	now := time.Now().UnixMilli()
	for i := range entities {
		e := &entities[i]

		aliases := e.Aliases
		if len(aliases) > MaxEntityAliases {
			aliases = aliases[:MaxEntityAliases]
		}
		aliasesJSON, err := json.Marshal(aliases)
		if err != nil {
			return err
		}
		for len(aliasesJSON) > maxAliasesJSON && len(aliases) > 0 {
			aliases = aliases[:len(aliases)-1]
			if aliasesJSON, err = json.Marshal(aliases); err != nil {
				return err
			}
		}

		propsJSON, err := json.Marshal(e.Properties)
		if err != nil {
			return err
		}

		updatedAt := e.UpdatedAt
		if updatedAt == 0 {
			updatedAt = now
		}

		if _, err := upsert.Exec(
			e.ID, e.EntityType, e.CanonicalName, e.CanonicalNameNormalized,
			string(aliasesJSON), string(propsJSON), nullableString(e.PackID), updatedAt,
		); err != nil {
			return err
		}

		var canonicalID string
		if err := lookup.QueryRow(e.EntityType, e.CanonicalNameNormalized).Scan(&canonicalID); err != nil {
			return err
		}
		e.ID = canonicalID
	}

	return tx.Commit()
}

// InsertEntityMentions inserts mentions; duplicates on (entity_id, node_id, line) are ignored.
func (s *Store) InsertEntityMentions(mentions []Mention) error {
	if len(mentions) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO entity_mentions (entity_id,node_id,file_path,line,context,mention_type,updated_at) VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UnixMilli()
	for _, m := range mentions {
		ctx := m.Context
		if len([]rune(ctx)) > MaxMentionContextLen {
			runes := []rune(ctx)
			ctx = string(runes[:MaxMentionContextLen])
		}
		mentionType := m.MentionType
		if mentionType == "" {
			mentionType = "reference"
		}
		updatedAt := m.UpdatedAt
		if updatedAt == 0 {
			updatedAt = now
		}
		if _, err := stmt.Exec(
			m.EntityID, m.NodeID, m.FilePath, m.Line,
			ctx, mentionType, updatedAt,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// DeleteEntityData removes all entity_mentions for filePath, then prunes orphan entities.
func (s *Store) DeleteEntityData(filePath string) error {
	if _, err := s.db.Exec(
		`DELETE FROM entity_mentions WHERE file_path = ?`, filePath,
	); err != nil {
		return err
	}
	return s.PruneOrphanEntities()
}

// PruneOrphanEntities deletes entities with no remaining entity_mentions rows.
func (s *Store) PruneOrphanEntities() error {
	_, err := s.db.Exec(
		`DELETE FROM entities
		 WHERE id NOT IN (SELECT DISTINCT entity_id FROM entity_mentions)`)
	return err
}

// GetEntityMentions returns mentions for a node_id, ordered by line.
func (s *Store) GetEntityMentions(nodeID string) ([]Mention, error) {
	rows, err := s.db.Query(`SELECT entity_id,node_id,file_path,line,context,mention_type,updated_at FROM entity_mentions WHERE node_id=? ORDER BY line`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Mention
	for rows.Next() {
		var m Mention
		if err := rows.Scan(
			&m.EntityID, &m.NodeID, &m.FilePath, &m.Line,
			&m.Context, &m.MentionType, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetEntityByID returns the entity with the given UUID, or nil if not found.
func (s *Store) GetEntityByID(entityID string) (*Entity, error) {
	row := s.db.QueryRow(`SELECT id,entity_type,canonical_name,canonical_name_normalized,aliases,properties,pack_id,updated_at FROM entities WHERE id=?`, entityID)

	var e Entity
	var aliasesJSON, propsJSON string
	var packID *string
	if err := row.Scan(
		&e.ID, &e.EntityType, &e.CanonicalName, &e.CanonicalNameNormalized,
		&aliasesJSON, &propsJSON, &packID, &e.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if packID != nil {
		e.PackID = *packID
	}

	if err := json.Unmarshal([]byte(aliasesJSON), &e.Aliases); err != nil {
		e.Aliases = nil
	}
	props := make(map[string]string)
	if err := json.Unmarshal([]byte(propsJSON), &props); err == nil {
		e.Properties = props
	}

	return &e, nil
}

// GetEntityStats returns total entity and mention counts.
func (s *Store) GetEntityStats() (entities int, mentions int, err error) {
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM entities`).Scan(&entities); err != nil {
		return 0, 0, err
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM entity_mentions`).Scan(&mentions); err != nil {
		return 0, 0, err
	}
	return entities, mentions, nil
}

// nullableString returns nil for an empty string (maps to SQL NULL).
func nullableString(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

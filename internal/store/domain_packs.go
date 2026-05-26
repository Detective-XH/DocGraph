package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Detective-XH/docgraph/internal/domainpacks"
)

// DomainPackStats reports the loaded pack surface for status output.
type DomainPackStats struct {
	TotalPacks    int
	EnabledPacks  int
	TotalFields   int
	BuiltInPacks  int
	OptionalPacks int
}

// SyncDomainPacks persists process-registered pack definitions into SQLite.
// Existing enabled flags are preserved so a future CLI can disable a pack
// without that choice being overwritten on every process start.
func (s *Store) SyncDomainPacks(packs []domainpacks.Pack) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin domain pack sync: %w", err)
	}
	defer tx.Rollback()

	packStmt, err := tx.Prepare(`
		INSERT INTO domain_packs(
			id, name, version, domain, enabled, builtin,
			min_schema_version, status, description, loaded_at, metadata
		) VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name               = excluded.name,
			version            = excluded.version,
			domain             = excluded.domain,
			builtin            = excluded.builtin,
			min_schema_version = excluded.min_schema_version,
			status             = excluded.status,
			description        = excluded.description,
			loaded_at          = excluded.loaded_at,
			metadata           = excluded.metadata
	`)
	if err != nil {
		return fmt.Errorf("prepare domain pack upsert: %w", err)
	}
	defer packStmt.Close()

	fieldStmt, err := tx.Prepare(`
		INSERT INTO domain_pack_fields(
			pack_id, field_key, column_name, value_type, required, aliases, description
		) VALUES(?,?,?,?,?,?,?)
	`)
	if err != nil {
		return fmt.Errorf("prepare domain pack field insert: %w", err)
	}
	defer fieldStmt.Close()

	now := time.Now().Unix()
	for _, pack := range packs {
		enabled := 0
		if pack.EnabledByDefault {
			enabled = 1
		}
		builtin := 0
		if pack.BuiltIn {
			builtin = 1
		}
		metadata := fmt.Sprintf(`{"field_count":%d}`, len(pack.Fields))
		if _, err := packStmt.Exec(
			pack.ID, pack.Name, pack.Version, pack.Domain, enabled, builtin,
			pack.MinSchemaVersion, pack.Status, pack.Description, now, metadata,
		); err != nil {
			return fmt.Errorf("upsert domain pack %q: %w", pack.ID, err)
		}
		if _, err := tx.Exec(`DELETE FROM domain_pack_fields WHERE pack_id = ?`, pack.ID); err != nil {
			return fmt.Errorf("clear domain pack fields %q: %w", pack.ID, err)
		}
		for _, field := range pack.Fields {
			aliases, err := json.Marshal(field.Aliases)
			if err != nil {
				return fmt.Errorf("marshal aliases for %q/%q: %w", pack.ID, field.Key, err)
			}
			required := 0
			if field.Required {
				required = 1
			}
			if _, err := fieldStmt.Exec(
				pack.ID, field.Key, field.Column, field.ValueType,
				required, string(aliases), field.Description,
			); err != nil {
				return fmt.Errorf("insert domain pack field %q/%q: %w", pack.ID, field.Key, err)
			}
		}
	}
	return tx.Commit()
}

// GetDomainPacks returns all persisted pack definitions with fields attached.
func (s *Store) GetDomainPacks() ([]domainpacks.Pack, error) {
	rows, err := s.db.Query(`
		SELECT id, name, version, domain, enabled, builtin,
		       min_schema_version, status, description
		FROM domain_packs
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	packs := []domainpacks.Pack{}
	packIndex := make(map[string]int)
	for rows.Next() {
		var pack domainpacks.Pack
		var enabled, builtin int
		if err := rows.Scan(
			&pack.ID, &pack.Name, &pack.Version, &pack.Domain, &enabled, &builtin,
			&pack.MinSchemaVersion, &pack.Status, &pack.Description,
		); err != nil {
			return nil, err
		}
		pack.EnabledByDefault = enabled == 1
		pack.BuiltIn = builtin == 1
		packIndex[pack.ID] = len(packs)
		packs = append(packs, pack)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	fieldRows, err := s.db.Query(`
		SELECT pack_id, field_key, column_name, value_type, required, aliases, description
		FROM domain_pack_fields
		ORDER BY pack_id, field_key
	`)
	if err != nil {
		return nil, err
	}
	defer fieldRows.Close()

	for fieldRows.Next() {
		var packID string
		var field domainpacks.Field
		var required int
		var aliasesJSON string
		if err := fieldRows.Scan(
			&packID, &field.Key, &field.Column, &field.ValueType,
			&required, &aliasesJSON, &field.Description,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(aliasesJSON), &field.Aliases); err != nil {
			return nil, fmt.Errorf("decode aliases for %q/%q: %w", packID, field.Key, err)
		}
		field.Required = required == 1
		if idx, ok := packIndex[packID]; ok {
			packs[idx].Fields = append(packs[idx].Fields, field)
		}
	}
	return packs, fieldRows.Err()
}

// IsPackEnabled reports whether the domain pack with the given ID is enabled in SQLite.
// Returns false (not an error) if the pack does not yet exist in the table.
func (s *Store) IsPackEnabled(packID string) (bool, error) {
	var enabled int
	err := s.db.QueryRow(`SELECT enabled FROM domain_packs WHERE id = ?`, packID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return enabled == 1, err
}

// SetPackEnabled updates the persisted enabled state for a domain pack.
// The pack must already be registered in domain_packs; Store.Open syncs
// process-registered packs before callers can reach this method.
func (s *Store) SetPackEnabled(packID string, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	res, err := s.db.Exec(`UPDATE domain_packs SET enabled = ?, loaded_at = ? WHERE id = ?`, value, time.Now().Unix(), packID)
	if err != nil {
		return fmt.Errorf("set domain pack %q enabled=%t: %w", packID, enabled, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check domain pack %q update: %w", packID, err)
	}
	if affected == 0 {
		return fmt.Errorf("domain pack %q is not registered", packID)
	}
	return nil
}

// DeleteFilesByNodeKind removes all files whose document node has the given kind.
// This is used when disabling opt-in indexed surfaces, such as code_doc, so stale
// nodes and section chunks do not remain searchable after the pack is turned off.
func (s *Store) DeleteFilesByNodeKind(kind string) (int, error) {
	rows, err := s.db.Query(`SELECT DISTINCT file_path FROM nodes WHERE kind = ?`, kind)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return 0, err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, path := range paths {
		if err := s.DeleteSectionChunksByFile(path); err != nil {
			return 0, fmt.Errorf("delete section chunks for %s: %w", path, err)
		}
		if err := s.DeleteDocumentMetadataByFile(path); err != nil {
			return 0, fmt.Errorf("delete metadata for %s: %w", path, err)
		}
		if err := s.DeleteEntityData(path); err != nil {
			return 0, fmt.Errorf("delete entity data for %s: %w", path, err)
		}
		if err := s.DeleteFileData(path); err != nil {
			return 0, fmt.Errorf("delete file data for %s: %w", path, err)
		}
	}
	return len(paths), nil
}

// GetDomainPackStats returns aggregate pack counts.
func (s *Store) GetDomainPackStats() (DomainPackStats, error) {
	var stats DomainPackStats
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM domain_packs`).Scan(&stats.TotalPacks); err != nil {
		return stats, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM domain_packs WHERE enabled = 1`).Scan(&stats.EnabledPacks); err != nil {
		return stats, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM domain_packs WHERE builtin = 1`).Scan(&stats.BuiltInPacks); err != nil {
		return stats, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM domain_pack_fields`).Scan(&stats.TotalFields); err != nil {
		return stats, err
	}
	stats.OptionalPacks = stats.TotalPacks - stats.BuiltInPacks
	return stats, nil
}

package store

import (
	"testing"

	"github.com/Detective-XH/docgraph/internal/domainpacks"
)

func TestSyncDomainPacksPersistsBuiltins(t *testing.T) {
	st := openTestStore(t)

	packs, err := st.GetDomainPacks()
	if err != nil {
		t.Fatalf("GetDomainPacks: %v", err)
	}
	if len(packs) != 2 {
		t.Fatalf("got %d packs, want 2", len(packs))
	}
	if packs[0].ID != domainpacks.PackGovernance {
		t.Fatalf("first pack = %q, want %q", packs[0].ID, domainpacks.PackGovernance)
	}
	if packs[1].ID != domainpacks.PackResearchProvenance {
		t.Fatalf("second pack = %q, want %q", packs[1].ID, domainpacks.PackResearchProvenance)
	}
	if len(packs[0].Fields) == 0 || len(packs[1].Fields) == 0 {
		t.Fatalf("expected fields for both built-in packs: %#v", packs)
	}

	stats, err := st.GetDomainPackStats()
	if err != nil {
		t.Fatalf("GetDomainPackStats: %v", err)
	}
	if stats.TotalPacks != 2 || stats.EnabledPacks != 2 || stats.BuiltInPacks != 2 || stats.OptionalPacks != 0 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}

func TestSyncDomainPacksPreservesDisabledState(t *testing.T) {
	st := openTestStore(t)

	if _, err := st.db.Exec(`UPDATE domain_packs SET enabled = 0 WHERE id = ?`, domainpacks.PackGovernance); err != nil {
		t.Fatalf("disable pack: %v", err)
	}
	if err := st.SyncDomainPacks(domainpacks.Packs()); err != nil {
		t.Fatalf("SyncDomainPacks: %v", err)
	}

	packs, err := st.GetDomainPacks()
	if err != nil {
		t.Fatalf("GetDomainPacks: %v", err)
	}
	for _, pack := range packs {
		if pack.ID == domainpacks.PackGovernance && pack.EnabledByDefault {
			t.Fatal("SyncDomainPacks overwrote disabled state")
		}
	}
}

func TestSyncDomainPacksAcceptsOptionalPack(t *testing.T) {
	st := openTestStore(t)

	pack := domainpacks.Pack{
		ID:               "client-deliverable",
		Name:             "Client Deliverable",
		Version:          "0.1.0",
		Domain:           "client_deliverable",
		Status:           "experimental",
		EnabledByDefault: false,
		MinSchemaVersion: 8,
		Fields: []domainpacks.Field{
			{Key: "deliverable_id", ValueType: "string"},
			{Key: "client", ValueType: "string"},
		},
	}
	reg := domainpacks.NewRegistry()
	if err := reg.Register(pack); err != nil {
		t.Fatalf("Register optional pack: %v", err)
	}
	if err := st.SyncDomainPacks(reg.Packs()); err != nil {
		t.Fatalf("SyncDomainPacks optional: %v", err)
	}

	packs, err := st.GetDomainPacks()
	if err != nil {
		t.Fatalf("GetDomainPacks: %v", err)
	}
	var found bool
	for _, got := range packs {
		if got.ID == "client_deliverable" {
			found = true
			if got.EnabledByDefault {
				t.Fatal("optional pack should be disabled by default")
			}
			if len(got.Fields) != 2 {
				t.Fatalf("optional pack fields = %d, want 2", len(got.Fields))
			}
		}
	}
	if !found {
		t.Fatal("optional pack was not persisted")
	}
}

func TestMigration008CreatesDomainPackTables(t *testing.T) {
	db := openRawDB(t)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	for _, tbl := range []string{"domain_packs", "domain_pack_fields"} {
		if !tableExists(db, tbl) {
			t.Fatalf("table %q not found after migration 008", tbl)
		}
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := t.TempDir() + "/docgraph.db"
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

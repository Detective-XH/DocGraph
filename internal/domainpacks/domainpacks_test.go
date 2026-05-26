package domainpacks

import "testing"

func TestRegistryRegisterAndSortsPackFields(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(Pack{
		ID:               "policy-pack",
		Name:             "Policy Pack",
		Version:          "1.0.0",
		Domain:           "policy",
		EnabledByDefault: true,
		Fields: []Field{
			{Key: "review_due", ValueType: "date"},
			{Key: "policy_id", ValueType: "string"},
		},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	packs := reg.Packs()
	if len(packs) != 1 {
		t.Fatalf("got %d packs, want 1", len(packs))
	}
	if packs[0].ID != "policy_pack" {
		t.Fatalf("normalized ID = %q, want policy_pack", packs[0].ID)
	}
	if packs[0].Fields[0].Key != "policy_id" || packs[0].Fields[1].Key != "review_due" {
		t.Fatalf("fields not sorted by key: %#v", packs[0].Fields)
	}

	cols := reg.FieldColumnMap("policy_pack")
	if cols["policy_id"] != "policy_id" || cols["review_due"] != "review_due" {
		t.Fatalf("unexpected field column map: %#v", cols)
	}
}

func TestRegistryRejectsDuplicatePack(t *testing.T) {
	reg := NewRegistry()
	pack := Pack{
		ID:      "legal",
		Version: "1.0.0",
		Domain:  "legal",
		Fields:  []Field{{Key: "matter_id", ValueType: "string"}},
	}
	if err := reg.Register(pack); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := reg.Register(pack); err == nil {
		t.Fatal("expected duplicate pack error")
	}
}

func TestBuiltinPacksExposeProjectionColumns(t *testing.T) {
	gov := FieldColumnMap(PackGovernance)
	if gov["status"] != "status" || gov["canonical_source"] != "canonical_source" {
		t.Fatalf("governance field map missing expected keys: %#v", gov)
	}
	research := FieldColumnMap(PackResearchProvenance)
	if research["claim_id"] != "claim_id" || research["deliverable_id"] != "deliverable_id" {
		t.Fatalf("research field map missing expected keys: %#v", research)
	}
	entity := FieldColumnMap(PackEntity)
	if entity["entity_type"] != "entity_type" || entity["canonical_name"] != "canonical_name" {
		t.Fatalf("entity field map missing expected keys: %#v", entity)
	}
}

package domainpacks

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	// PackGovernance exposes the F-21 governance metadata schema as a domain pack.
	PackGovernance = "governance"

	// PackResearchProvenance exposes the F-22 research provenance schema as a domain pack.
	PackResearchProvenance = "research_provenance"

	// PackEntity exposes the F-29 entity and source graph schema as a domain pack.
	PackEntity = "entity"
)

// Field describes one metadata key owned by a domain schema pack.
type Field struct {
	Key         string
	Column      string
	ValueType   string
	Required    bool
	Aliases     []string
	Description string
}

// Pack is a declarative schema pack. Packs define metadata fields and optional
// typed projection columns without owning core schema migrations directly.
type Pack struct {
	ID               string
	Name             string
	Version          string
	Domain           string
	Description      string
	Status           string
	BuiltIn          bool
	EnabledByDefault bool
	MinSchemaVersion int
	Fields           []Field
}

// Registry stores pack definitions contributed by core or optional packages.
type Registry struct {
	mu    sync.RWMutex
	packs map[string]Pack
}

var defaultRegistry = NewRegistry()

func init() {
	mustRegister(BuiltinGovernancePack())
	mustRegister(BuiltinResearchProvenancePack())
	mustRegister(BuiltinEntityPack())
}

// NewRegistry returns an empty pack registry.
func NewRegistry() *Registry {
	return &Registry{packs: make(map[string]Pack)}
}

// Register adds a pack to the process-wide registry. Optional packages can call
// this from init() to make their schema available to DocGraph.
func Register(pack Pack) error {
	return defaultRegistry.Register(pack)
}

// Packs returns all process-wide packs sorted by ID.
func Packs() []Pack {
	return defaultRegistry.Packs()
}

// EnabledPacks returns process-wide packs enabled by default, sorted by ID.
func EnabledPacks() []Pack {
	return defaultRegistry.EnabledPacks()
}

// FieldColumnMap returns metadata-key to projection-column mapping for a pack.
func FieldColumnMap(packID string) map[string]string {
	return defaultRegistry.FieldColumnMap(packID)
}

// Register validates and stores a pack in this registry.
func (r *Registry) Register(pack Pack) error {
	normalized, err := normalizePack(pack)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.packs[normalized.ID]; exists {
		return fmt.Errorf("domain pack %q already registered", normalized.ID)
	}
	r.packs[normalized.ID] = normalized
	return nil
}

// Packs returns all registered packs sorted by ID.
func (r *Registry) Packs() []Pack {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Pack, 0, len(r.packs))
	for _, pack := range r.packs {
		out = append(out, clonePack(pack))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// EnabledPacks returns packs whose default state is enabled.
func (r *Registry) EnabledPacks() []Pack {
	packs := r.Packs()
	out := packs[:0]
	for _, pack := range packs {
		if pack.EnabledByDefault {
			out = append(out, pack)
		}
	}
	return out
}

// FieldColumnMap returns a copy of the projection mapping for packID.
func (r *Registry) FieldColumnMap(packID string) map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pack, ok := r.packs[packID]
	if !ok {
		return nil
	}
	out := make(map[string]string, len(pack.Fields))
	for _, field := range pack.Fields {
		col := field.Column
		if col == "" {
			col = field.Key
		}
		out[field.Key] = col
	}
	return out
}

func mustRegister(pack Pack) {
	if err := Register(pack); err != nil {
		panic(err)
	}
}

func normalizePack(pack Pack) (Pack, error) {
	pack.ID = normalizeToken(pack.ID)
	pack.Domain = normalizeToken(pack.Domain)
	pack.Status = normalizeToken(pack.Status)
	pack.Version = strings.TrimSpace(pack.Version)
	pack.Name = strings.TrimSpace(pack.Name)
	pack.Description = strings.TrimSpace(pack.Description)
	if pack.ID == "" {
		return Pack{}, fmt.Errorf("domain pack id is required")
	}
	if pack.Name == "" {
		pack.Name = pack.ID
	}
	if pack.Version == "" {
		return Pack{}, fmt.Errorf("domain pack %q version is required", pack.ID)
	}
	if pack.Domain == "" {
		return Pack{}, fmt.Errorf("domain pack %q domain is required", pack.ID)
	}
	if pack.Status == "" {
		pack.Status = "stable"
	}
	seen := make(map[string]bool, len(pack.Fields))
	for i := range pack.Fields {
		field := &pack.Fields[i]
		field.Key = normalizeToken(field.Key)
		field.Column = normalizeToken(field.Column)
		field.ValueType = normalizeToken(field.ValueType)
		field.Description = strings.TrimSpace(field.Description)
		if field.Key == "" {
			return Pack{}, fmt.Errorf("domain pack %q field key is required", pack.ID)
		}
		if seen[field.Key] {
			return Pack{}, fmt.Errorf("domain pack %q has duplicate field %q", pack.ID, field.Key)
		}
		seen[field.Key] = true
		if field.Column == "" {
			field.Column = field.Key
		}
		if !validValueType(field.ValueType) {
			return Pack{}, fmt.Errorf("domain pack %q field %q has invalid value_type %q", pack.ID, field.Key, field.ValueType)
		}
		for j := range field.Aliases {
			field.Aliases[j] = normalizeToken(field.Aliases[j])
		}
		sort.Strings(field.Aliases)
	}
	sort.Slice(pack.Fields, func(i, j int) bool { return pack.Fields[i].Key < pack.Fields[j].Key })
	return pack, nil
}

func normalizeToken(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, "-", "_")
	return s
}

func validValueType(valueType string) bool {
	switch valueType {
	case "string", "number", "date", "bool", "list", "ref":
		return true
	default:
		return false
	}
}

func clonePack(pack Pack) Pack {
	pack.Fields = append([]Field(nil), pack.Fields...)
	for i := range pack.Fields {
		pack.Fields[i].Aliases = append([]string(nil), pack.Fields[i].Aliases...)
	}
	return pack
}

// BuiltinGovernancePack returns the current governance schema as a pack.
func BuiltinGovernancePack() Pack {
	return Pack{
		ID:               PackGovernance,
		Name:             "Governance Metadata",
		Version:          "1.0.0",
		Domain:           "governance",
		Description:      "Typed governance metadata for status, stewardship, review, sensitivity, and canonical-source controls.",
		Status:           "stable",
		BuiltIn:          true,
		EnabledByDefault: true,
		MinSchemaVersion: 6,
		Fields: []Field{
			{Key: "allowed_audience", Column: "allowed_audience", ValueType: "list", Description: "Allowed audience labels for retrieval boundaries."},
			{Key: "approver", Column: "approver", ValueType: "string", Description: "Person or role that approved the document."},
			{Key: "canonical_source", Column: "canonical_source", ValueType: "string", Description: "Authoritative source marker when duplicates exist."},
			{Key: "department", Column: "department", ValueType: "string", Description: "Owning department or business unit."},
			{Key: "effective_date", Column: "effective_date", ValueType: "date", Description: "Date the document becomes operationally valid."},
			{Key: "owner", Column: "owner", ValueType: "string", Description: "Person or role accountable for the document."},
			{Key: "review_due", Column: "review_due", ValueType: "date", Description: "Date by which the document needs review."},
			{Key: "sensitivity", Column: "sensitivity", ValueType: "string", Description: "Sensitivity classification for retrieval boundaries."},
			{Key: "status", Column: "status", ValueType: "string", Description: "Governance lifecycle status."},
			{Key: "superseded_by", Column: "superseded_by", ValueType: "ref", Description: "Document that replaces this document."},
			{Key: "supersedes", Column: "supersedes", ValueType: "ref", Description: "Document replaced by this document."},
		},
	}
}

// BuiltinEntityPack returns the F-29 entity and source graph schema as a pack.
func BuiltinEntityPack() Pack {
	return Pack{
		ID:               PackEntity,
		Name:             "Generic Entity Graph",
		Version:          "1.0.0",
		Domain:           "entity",
		Description:      "Entity and source graph metadata for named entities, canonical names, and alias resolution.",
		Status:           "stable",
		BuiltIn:          true,
		EnabledByDefault: true,
		MinSchemaVersion: 10,
		Fields: []Field{
			{Key: "aliases", Column: "aliases", ValueType: "list", Description: "Alternative names for this entity."},
			{Key: "canonical_name", Column: "canonical_name", ValueType: "string", Description: "Canonical entity name."},
			{Key: "entity_type", Column: "entity_type", ValueType: "string", Description: "Entity classification (person, organization, location, etc.)."},
		},
	}
}

// BuiltinResearchProvenancePack returns the current research schema as a pack.
func BuiltinResearchProvenancePack() Pack {
	return Pack{
		ID:               PackResearchProvenance,
		Name:             "Research Provenance",
		Version:          "1.0.0",
		Domain:           "research",
		Description:      "Typed research provenance metadata for claims, evidence, confidence, verification, clients, and deliverables.",
		Status:           "stable",
		BuiltIn:          true,
		EnabledByDefault: true,
		MinSchemaVersion: 7,
		Fields: []Field{
			{Key: "analyst_status", Column: "analyst_status", ValueType: "string", Description: "Analyst workflow status for the claim or assessment."},
			{Key: "assessment_date", Column: "assessment_date", ValueType: "date", Description: "Date the assessment was produced."},
			{Key: "claim_id", Column: "claim_id", ValueType: "string", Description: "Stable identifier for a research claim."},
			{Key: "client", Column: "client", ValueType: "string", Description: "Client or recipient associated with the work."},
			{Key: "confidence", Column: "confidence", ValueType: "string", Description: "Analyst confidence label."},
			{Key: "deliverable_id", Column: "deliverable_id", ValueType: "string", Description: "Deliverable identifier linked to the research."},
			{Key: "event_date", Column: "event_date", ValueType: "date", Description: "Date of the underlying event."},
			{Key: "evidence", Column: "evidence", ValueType: "list", Description: "Evidence references or citations supporting the claim."},
			{Key: "last_verified", Column: "last_verified", ValueType: "date", Description: "Date the evidence or claim was last verified."},
			{Key: "source_type", Column: "source_type", ValueType: "string", Description: "Source category such as primary, secondary, or internal."},
			{Key: "valid_until", Column: "valid_until", ValueType: "date", Description: "Date after which the assessment needs refresh."},
		},
	}
}

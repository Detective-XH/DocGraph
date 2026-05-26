package domainpacks

// PackPolicyProcess is the ID of the F-30 policy/process drift audit pack.
const PackPolicyProcess = "policy_process"

func init() {
	mustRegister(BuiltinPolicyProcessPack())
}

// BuiltinPolicyProcessPack returns the F-30 policy/process drift audit schema as a
// bundled optional pack. EnabledByDefault is false — users opt in per project.
func BuiltinPolicyProcessPack() Pack {
	return Pack{
		ID:               PackPolicyProcess,
		Name:             "Policy and Process Drift Audit",
		Version:          "1.0.0",
		Domain:           "policy_process",
		Description:      "Detects conflicting, stale, duplicated, superseded, non-canonical, or review-overdue policy/process documents using governance metadata, similarity edges, and reference graph.",
		Status:           "stable",
		BuiltIn:          true,
		EnabledByDefault: false,
		MinSchemaVersion: 10,
		Fields: []Field{
			{Key: "sop_category", ValueType: "string", Description: "Standard operating procedure category."},
			{Key: "policy_domain", ValueType: "string", Description: "Policy classification domain (e.g. HR, Security, Finance)."},
			{Key: "process_owner", ValueType: "string", Description: "Role or team responsible for this process or SOP."},
			{Key: "version", ValueType: "string", Description: "Policy or process version number."},
			{Key: "conflict_resolution", ValueType: "string", Description: "How conflicts with this document should be resolved."},
		},
	}
}

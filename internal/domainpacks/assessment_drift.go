package domainpacks

// PackAssessmentDrift is the ID of the assessment drift audit pack.
const PackAssessmentDrift = "assessment_drift"

func init() {
	mustRegister(BuiltinAssessmentDriftPack())
}

// BuiltinAssessmentDriftPack returns the assessment drift audit schema as a
// bundled optional pack. EnabledByDefault is false — users opt in per project.
func BuiltinAssessmentDriftPack() Pack {
	return Pack{
		ID:               PackAssessmentDrift,
		Name:             "Assessment Drift and Contradiction Audit",
		Version:          "1.0.0",
		Domain:           "research",
		Description:      "Detects stale assessments, unverified evidence, competing interpretations, superseded research claims, and impacted deliverables using research provenance metadata and the reference graph.",
		Status:           "stable",
		BuiltIn:          true,
		EnabledByDefault: false,
		MinSchemaVersion: 7,
		Fields: []Field{
			{Key: "contradicts", ValueType: "ref", Description: "Reference to a document whose conclusions conflict with this one."},
			{Key: "supersedes_claim", ValueType: "ref", Description: "Claim ID or document superseded by this assessment."},
		},
	}
}

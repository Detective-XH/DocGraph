---
name: assessment-drift-audit
description: Run the assessment/research drift audit. Surfaces stale assessments, unverified evidence, competing interpretations, superseded research claims, and impacted deliverables using DocGraph's built-in drift engine. Presents findings and optional remediation suggestions without writing research decisions.
triggers:
  - assessment audit
  - assessment drift
  - research drift
  - research audit
  - stale assessment
  - competing interpretations
  - impacted deliverable
---

# Assessment/Research Drift Audit

> **Invocation** — Claude Code: `Skill("assessment-drift-audit")` | OpenCode / Codex: use triggers above.

Runs the DocGraph assessment drift audit and presents findings for human review.
The audit is advisory — findings highlight candidates for action, not authoritative rulings.

## Execution

- Model: Haiku (query) → Sonnet (remediation suggestions if requested)
- Effort: Low
- Advisor: Not required
- Core computation: done by DocGraph's `GetDriftFindings` engine, not by this skill

---

## Step 1: Check index status

```
docgraph_status
```

Confirm:
- Schema version is v7 or higher (research_metadata migration applied)
- `assessment_drift` pack is listed under Domain Packs
- Research metadata is indexed (docs with research metadata > 0)

If schema version < 7, tell the user to run `docgraph index --force` first.

---

## Step 2: Run the drift audit

```
docgraph_context task="research/assessment drift audit" format=drift_audit
```

This calls DocGraph's `GetDriftFindings` engine, which checks:

| Code | Meaning |
|------|---------|
| `research.stale_assessment` | `valid_until` date has passed; assessment is expired |
| `research.unverified_evidence` | `last_verified` date is older than 180 days (configurable) |
| `research.competing_interpretations` | Multiple documents share the same `claim_id` but have conflicting `confidence` or `analyst_status` |
| `research.superseded_claim` | A research document with `superseded_by` set is still referenced by other active documents |
| `research.impacted_deliverable` | A deliverable (`deliverable_id` set) is linked to an expired assessment |

---

## Step 3: Present findings to the user

Group findings by severity (warnings first, then info).

For each finding:
1. State the **file path(s)** and **finding code**
2. Show the **message** and **evidence** from the finding
3. Suggest a concrete remediation (below) — but do NOT make the change without confirmation

### Remediation patterns

| Code | Suggested action |
|------|-----------------|
| `research.stale_assessment` | Update `valid_until` in frontmatter, or add a new superseding assessment |
| `research.unverified_evidence` | Re-verify the evidence and update `last_verified` in frontmatter |
| `research.competing_interpretations` | Reconcile the conflicting documents; update `confidence` and `analyst_status` to reflect the resolved position |
| `research.superseded_claim` | Update citing documents to reference the newer version instead |
| `research.impacted_deliverable` | Review the deliverable against the expired assessment; update or note the dependency |

---

## Step 4: Optional — deep-dive a specific document

To review a specific flagged document in detail:

```
docgraph_node path="<file-path>"
```

This shows research metadata, incoming/outgoing references, and section content.

---

## Skill boundary

This skill:
- **Reads** findings from DocGraph's engine (no independent analysis)
- **Suggests** frontmatter patches for human approval
- **Does NOT** write research decisions, invent authority, or set `confidence`/`analyst_status` without explicit user confirmation
- **Does NOT** modify any files without user approval

Research authority remains with the document owners and analysts. This skill is a display and triage layer.

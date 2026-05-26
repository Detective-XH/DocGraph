---
name: policy-drift-audit
description: Run the policy/process drift audit. Surfaces stale, superseded, duplicate, non-canonical, and conflicting policy documents using DocGraph's built-in drift engine. Presents findings and optional remediation suggestions without writing governance decisions.
triggers:
  - policy audit
  - policy drift
  - process drift
  - sop audit
  - drift audit policy
---

# Policy/Process Drift Audit

> **Invocation** — Claude Code: `Skill("policy-drift-audit")` | OpenCode / Codex: use triggers above.

Runs the DocGraph policy/process drift audit and presents findings for human review.
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
- Schema version is v10 or higher (entity_source_graph migration applied)
- `policy_process` pack is listed under Domain Packs
- Governance metadata is indexed (docs with metadata > 0)

If schema version < 10, tell the user to run `docgraph index --force` first.

---

## Step 2: Run the drift audit

```
docgraph_context task="policy/process drift audit" format=drift_audit
```

This calls DocGraph's `GetDriftFindings` engine, which checks:

| Code | Meaning |
|------|---------|
| `policy.stale_review` | `review_due` date has passed; status is not archived/superseded |
| `policy.superseded_referenced` | A superseded document is still referenced by an active document |
| `policy.duplicate` | Two approved documents are highly similar (≥ 0.75 similarity) |
| `policy.non_canonical` | Multiple active documents claim the same `canonical_source` |
| `policy.conflicting` | Competing active authorities on the same topic, or conflicting `supersedes` claims |

---

## Step 3: Present findings to the user

Group findings by severity (errors first, then warnings).

For each finding:
1. State the **file path(s)** and **finding code**
2. Show the **message** and **evidence** from the finding
3. Suggest a concrete remediation (below) — but do NOT make the change without confirmation

### Remediation patterns

| Code | Suggested action |
|------|-----------------|
| `policy.stale_review` | Update `review_due` in frontmatter, or change `status` to `archived` if retired |
| `policy.superseded_referenced` | Update the citing document to reference the newer version instead |
| `policy.duplicate` | Merge or archive one copy; ensure one declares itself `canonical_source` |
| `policy.non_canonical` | Resolve which copy is authoritative; archive or update `canonical_source` on the others |
| `policy.conflicting` | Determine which document holds authority; update `status`/`supersedes`/`superseded_by` accordingly |

---

## Step 4: Optional — deep-dive a specific document

To review a specific flagged document in detail:

```
docgraph_node path="<file-path>"
```

This shows governance metadata, incoming/outgoing references, and section content.

---

## Skill boundary

This skill:
- **Reads** findings from DocGraph's engine (no independent analysis)
- **Suggests** frontmatter patches for human approval
- **Does NOT** write governance decisions, invent authority, or set `status`/`owner` without explicit user confirmation
- **Does NOT** modify any files without user approval

Governance authority remains with the document owners. This skill is a display and triage layer.

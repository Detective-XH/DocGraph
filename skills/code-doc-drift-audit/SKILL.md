---
name: code-doc-drift-audit
description: Run the docs-code drift audit. Surfaces code files with no documentation references, broken doc-to-code links, and approved/review docs with no code anchors. Requires the code_doc domain pack to be enabled. Presents findings and optional remediation suggestions without writing code or governance decisions.
triggers:
  - code doc audit
  - docs code drift
  - undocumented export
  - missing symbol audit
  - code documentation drift
---

# Docs-Code Drift Audit

> **Invocation** — Claude Code: `Skill("code-doc-drift-audit")` | OpenCode / Codex: use triggers above.

Runs the DocGraph docs-code drift audit and presents findings for human review.
The audit is advisory — findings highlight candidates for action, not authoritative rulings.

## Execution

- Model: Haiku (query) → Sonnet (remediation suggestions if requested)
- Effort: Low
- Advisor: Not required
- Core computation: done by DocGraph's `GetDriftFindings` engine, not by this skill

---

## Step 1: Check prerequisites

```
docgraph_status
```

Confirm:
- `code_doc` pack is listed under Domain Packs and is **enabled**
- At least one `code_file` node is indexed (check node count or run `docgraph_search kind=code_file`)

If `code_doc` is not enabled, tell the user to run:
```bash
docgraph pack enable code_doc <project-path>
# or for workspace mode:
docgraph pack enable --workspace code_doc <workspace-path>
```

Then wait for the sync to complete before proceeding.

---

## Step 2: Run the drift audit

```
docgraph_context task="docs-code drift audit" format=drift_audit
```

This calls DocGraph's `GetDriftFindings` engine, which checks (code_doc findings only):

| Code | Severity | Meaning |
|------|----------|---------|
| `code.missing_symbol` | warning | A doc links to a code file path that is not indexed — broken doc-to-code reference |
| `code.undocumented_export` | info | A code file has no incoming `references`, `wikilinks_to`, or `related_to` edges from any doc — no documentation points to it |
| `code.unanchored_feature` | info | An approved or review-status doc has no outgoing edge to any indexed code file — feature doc not connected to implementation |

If `code_doc` is disabled, the output will show zero `code.*` findings regardless of repo content.

---

## Step 3: Present findings to the user

Group findings by code, severity first (warnings before info).

For each finding:
1. State the **file path(s)** and **finding code**
2. Show the **message** and **evidence** from the finding
3. Suggest a concrete remediation (below) — but do NOT make the change without confirmation

### Remediation patterns

| Code | Suggested action |
|------|-----------------|
| `code.missing_symbol` | Fix the broken link in the doc to point to the correct (indexed) code path, or enable `code_doc` if the target file isn't yet indexed |
| `code.undocumented_export` | Add a Markdown doc that references the code file with a link or wikilink, or add the code file path to an existing relevant doc |
| `code.unanchored_feature` | Add a reference or wikilink from the feature doc to the relevant implementation file(s) |

**Note on `code.unanchored_feature`:** this finding only fires when governance metadata is present (frontmatter `status: approved` or `status: review`). Docs without governance frontmatter are not checked.

---

## Step 4: Optional — inspect a specific file

To review a flagged code file or doc in detail:

```
docgraph_node path="<file-path>"
```

To see what currently references a code file:

```
docgraph_graph operation=incoming document="<code-file-path>"
```

---

## Skill boundary

This skill:
- **Reads** findings from DocGraph's engine (no independent analysis)
- **Suggests** doc edits or link additions for human approval
- **Does NOT** modify source code, governance status, or any file without explicit user confirmation
- **Does NOT** own call graphs, type resolution, or code semantics — use CodeGraph for those

Code intelligence (symbol lookup, callers, call traces) remains in CodeGraph's domain. This skill surfaces documentation coverage gaps only.

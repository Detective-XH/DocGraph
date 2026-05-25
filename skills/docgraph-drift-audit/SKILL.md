---
name: docgraph-drift-audit
description: Audit all .md files for DocGraph compatibility. Checks frontmatter, cross-references, heading structure, and wikilink conventions. Reports drift and suggests fixes.
triggers:
  - docgraph drift
  - docgraph audit
  - md audit
  - markdown audit
---

# DocGraph Drift Audit

> **Invocation** — Claude Code: `Skill("docgraph-drift-audit")` | OpenCode / Codex: use triggers above.

Scans all indexed .md files and reports how well they conform to DocGraph conventions.
Run after bulk content changes, after adding new docs, or when graph queries return
unexpected results.

## Execution

- Model: Haiku (scan) or Sonnet (fix suggestions)
- Effort: Low-Medium
- Advisor: Not required

---

## Step 1: Get index state

```
docgraph_status
```

Record file count, node count, edge count, unresolved refs count.
If unresolved refs > 5% of total edges, expect widespread broken-link findings below.

Locate the database for raw queries:

```bash
DB=$(find . -name "docgraph.db" -path "*/.docgraph/*" | head -1)
echo "DB: $DB"
```

---

## Step 2: Scan drift categories

### 2a. No frontmatter

```bash
sqlite3 "$DB" "SELECT path FROM files WHERE has_frontmatter = 0 ORDER BY path"
```

**PASS** if count = 0 for ADR/plan/governance docs.
Priority: HIGH for structured docs. LOW for README/changelog.

### 2b. No outgoing links — isolated docs

```bash
sqlite3 "$DB" "
  SELECT f.path FROM files f
  LEFT JOIN edges e ON e.source = f.path
    AND e.kind IN ('references','wikilinks_to','related_to')
  WHERE e.id IS NULL ORDER BY f.path"
```

**PASS** if key docs have at least one outgoing link.
Priority: HIGH if doc should relate to others. LOW for standalone entries.

### 2c. Broken links — unresolved refs

```bash
sqlite3 "$DB" "
  SELECT reference_text, reference_kind, COUNT(*) as cnt
  FROM unresolved_refs
  GROUP BY reference_text, reference_kind
  ORDER BY cnt DESC LIMIT 20"
```

**PASS** if count = 0. Broken links are graph bugs — HIGH priority always.

### 2d. No headings

```bash
sqlite3 "$DB" "
  SELECT f.path FROM files f
  LEFT JOIN nodes n ON n.file_path = f.path AND n.kind = 'heading'
  WHERE n.id IS NULL ORDER BY f.path"
```

**PASS** if count = 0. Priority: MEDIUM.

### 2e. Similarity islands — no similar_to edges

```bash
sqlite3 "$DB" "
  SELECT n.name FROM nodes n
  WHERE n.kind = 'document'
    AND n.id NOT IN (SELECT source FROM edges WHERE kind = 'similar_to')
    AND n.id NOT IN (SELECT target FROM edges WHERE kind = 'similar_to')
  ORDER BY n.name"
```

**PASS** if islands < 10% of total docs. If many, lower `--threshold` on next sync.
Priority: LOW.

---

## Step 3: Report

```
DocGraph Drift Audit — <project>
Date: YYYY-MM-DD
Index: N files, N nodes, N edges, N unresolved

Category                    Count   Status    Priority
No frontmatter              N       PASS/FAIL HIGH/LOW
No outgoing links           N       PASS/FAIL HIGH/LOW
Broken links (unresolved)   N       PASS/FAIL HIGH
No headings                 N       PASS/FAIL MEDIUM
Similarity islands          N       PASS/FAIL LOW

Top broken links:
  [[target]] × N
  path/file.md × N

Actions:
1. Add frontmatter to: [HIGH priority files]
2. Fix broken links: [with suggested corrections]
3. Add [[wikilinks]] to: [related but unlinked files]
```

---

## Step 4: Offer auto-fix

- **Frontmatter**: generate `---\ntags:\n  - <inferred>\nrelated_to:\n  - "[[related]]"\n---`
- **Broken links**: use `docgraph_files` to browse indexed files and find the correct target
- **Missing links**: use `docgraph_similar` to find topically related docs to link to

Show diff before applying. Never auto-commit.

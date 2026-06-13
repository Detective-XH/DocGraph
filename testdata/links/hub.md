---
title: Hub
related_to:
  - "[[target]]"
canonical_source: "[[hub]]"
---

# Hub

Fixture for ax-assert A11. Its frontmatter emits two document-level `wikilinks_to`
edges — one to another document (`target.md`) and one self-reference (its own
`canonical_source`). So the outgoing edge-row count (2) exceeds the distinct
other-document count (1), which makes `docgraph_graph operation=outgoing` render
the all-shown "report the distinct-document count, not the edge-row count" footer.

---
description: Show the correct next provenance action for the current repo state
---

Determine and report the correct next provenance action for this repository, rendering from the linespec advice engine.

- If the user passed file paths in `$ARGUMENTS`, run `linespec provenance next --plan $ARGUMENTS` to plan governance for those files (intent-aware, before editing).
- Otherwise run `linespec provenance next` for the ambient next action based on staged and working-tree changes.

Report the recommended command and the one-line reason verbatim. Do not invent guidance — if the command surfaces governing records, note that touching a governed file does NOT require superseding it; supersede only when deliberately revising a captured decision.

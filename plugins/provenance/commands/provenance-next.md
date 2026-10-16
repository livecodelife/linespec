---
description: Show the correct next provenance action for the current repo state
---

Determine and report the correct next provenance action for this repository, rendering from the provenance advice engine.

- If the user passed file paths in `$ARGUMENTS`, run `provenance next $ARGUMENTS` to plan governance for those files (before editing).
- Otherwise run `provenance next` for the ambient next action based on staged and working-tree changes.

Report the recommended command and the one-line reason verbatim. Do not invent guidance. If the command surfaces governing records, note that touching a governed file does not by itself require a new version of each: revise a record only when you are deliberately changing the decision it captured.

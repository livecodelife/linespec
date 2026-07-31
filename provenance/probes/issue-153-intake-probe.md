# Issue #153 intake probe

Proof artifact for prov-2026-75710391.

Issue #153 ("Test: intake dry-run probe") was a disposable issue used to
verify that the Job D intake pipeline detects a new GitHub issue and
scaffolds a corresponding provenance record. This file's existence, plus
the populated fields on prov-2026-75710391.yml, is the proof: the pipeline
ran, a `type: bug` record was created from the issue with no `implements`
parent, and no application source files were touched to resolve it.

The source issue is safe to close.

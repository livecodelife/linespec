# Bug Repro: discover skips non-route directories in framework mode

## Observed

Running `linespec provenance discover --dir <monorepo-package>` on a Go/Chi service
with ~40 subdirectories produces only 8 blueprint records.

## Root cause (two-part)

### 1. Framework path only covers route-containing files

`runDiscover` (main_stable.go) calls `assembler.Assemble()` which walks source files and
extracts only HTTP route registrations (`r.Get(...)`, `r.Post(...)`, etc.). Files with
no route registrations — services, repositories, models, utilities — are never added to
any group and receive no blueprint coverage.

The agnostic fallback (`runDiscoverAgnostic`) does the right thing — one group per
directory — but it is only called when *no* framework is detected. Once chi/rails/sinatra
is found, the agnostic path never runs.

### 2. `grouping_strategy: package` collapses too aggressively

`chi.yml` uses `grouping_strategy: package` (line 51). All routes in files sharing the
same Go package name collapse into a single blueprint group. A service with 8 distinct
Go packages that contain routes produces exactly 8 blueprints, regardless of how many
files or subdirectories exist.

## Reproduction steps

1. Run discover on any Go/Chi service with multiple non-route packages:
   ```
   linespec provenance discover --dry-run --dir path/to/chi-service
   ```
2. Observe: blueprint count ≈ number of unique Go packages with route registrations.
   All directories containing only service/repo/model/util code produce zero records.

## Files implicated

- `cmd/linespec/main_stable.go` — `runDiscover` never calls the agnostic supplemental pass
- `pkg/discover/framework/builtin/chi.yml` — `grouping_strategy: package` merges too broadly
- `pkg/discover/graph/graph.go` — `DirectoryGrouper` is the correct grouper but is unused
  in the framework path

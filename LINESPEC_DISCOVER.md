# `linespec provenance discover` — Command Summary

## Purpose

Bootstrap provenance records and LineSpec spec stubs for an existing codebase by statically analyzing source code and git history. Solves the cold-start problem when adopting LineSpec/Provenance on a legacy project: instead of starting from a blank page, teams run one command and get a draft provenance graph to review and refine.

## Commands

One new command with two modes of operation.

### `linespec provenance discover`

Scans the repo (or a targeted directory) and produces draft provenance records and LineSpec spec stubs.

```
linespec provenance discover [--dir path/to/scope] [--lang go|ruby] [--framework chi|rails|sinatra] [--enrich] [--dry-run] [--format table|json]
```

**Flags:**

- `--dir` — Scope the scan to a specific directory (e.g., a single service in a monorepo). Without this, scans the whole repo.
- `--lang` — Language of the target codebase. Auto-detected from file extensions and dependency manifests (go.mod, Gemfile, etc.) if omitted.
- `--framework` — Framework to use for route/middleware recognition. Auto-detected from dependencies if omitted. Explicit flag overrides detection.
- `--enrich` — Enable git history analysis to populate `intent` fields from commit messages and PR titles. Without this, only structurally-derived data is used.
- `--dry-run` — Print what would be generated without writing files. Shows the discovered routes, protocol boundaries, and proposed record groupings.
- `--format` — Output format for `--dry-run` (table for humans, json for tooling).

**Output:**

- Draft blueprint records in `provenance/` with populated `affected_scope`, `associated_specs`, and (with `--enrich`) `intent` fields.
- Skeleton `.linespec` files with `RECEIVE`, `EXPECT`, and `RESPOND` stubs derived from detected protocol boundaries.
- A summary report showing what was discovered: route count, protocol boundary count, records created, and any files that couldn't be classified.

No `linespec provenance discover watch` or incremental mode in v1. The change-driven use case (re-discover on touched files) can be a fast-follow, but the primary value is the initial full scan.

### Framework-agnostic mode

`discover` has two modes. The **framework-driven** mode above runs whenever a
supported framework is detected (or forced with `--framework`): it recognizes
routes and protocol boundaries and emits `.linespec` stubs alongside the records.

When no `--framework` is given and none of the built-in framework descriptions
match the codebase, `discover` falls back to **framework-agnostic mode** instead
of failing. It prints a notice and proceeds with structure-only analysis:

- **Language detection** by file extension and dependency manifests (respecting
  `--lang` when given), and **symbol extraction** for the detected language.
- Files are **grouped by directory**, and one **draft blueprint record** is
  written per group, with `affected_scope` set to that directory's source files.
- **No routes, protocol boundaries, or `.linespec` stubs are produced** — there
  is no framework description to derive them from, so the records capture *what
  code exists and how it clusters*, not its HTTP surface or cross-service calls.

This makes `discover` useful on any codebase — a library, a CLI, or a service in
a language/framework LineSpec doesn't yet have a description for — as a
directory-level provenance skeleton to refine by hand. Adding a framework
description (see [Shared Framework Descriptions](#shared-framework-descriptions-and-sedum-integration))
upgrades that same project to the full route- and boundary-aware mode. `--dry-run`
and `--format table|json` work identically in both modes.

---

## Technical Approach

### Parsing Infrastructure: Tree-sitter

All source parsing uses tree-sitter as a universal substrate rather than language-native AST tooling. Tree-sitter provides production-quality grammars for Go, Ruby, Python, TypeScript, Java, and most other languages, with a consistent query language (S-expression patterns) across all of them. Go bindings exist via go-tree-sitter.

This means one parsing infrastructure serves every language. Adding a new language to discover requires writing tree-sitter queries for that language's framework patterns, not building a new parser or integrating a new AST library. The same queries also serve Sedum's world state scanner (see "Shared Framework Descriptions" below).

Tree-sitter parses into a concrete syntax tree — it sees the actual source tokens, not a simplified abstract tree. This matters for Ruby and other dynamic languages where the AST packages that exist are less mature or require a runtime dependency. Tree-sitter grammars are maintained by the editor ecosystem (VS Code, Neovim, Helix, Zed all use them), so they stay current with language evolution.

**Trade-off vs. language-native tooling:** For Go specifically, `go/ast` + `go/types` offers richer semantic information — full type resolution across packages, resolved call graphs, interface satisfaction. Tree-sitter operates on syntax, not semantics: it can see that `r.Post("/path", handler)` is a method call, but it can't resolve that `r` is of type `chi.Router` through three layers of function arguments. This means protocol boundary tracing (Phase 2) is less precise than it would be with Go-native tooling. The trade-off is worthwhile: one infrastructure for all languages beats per-language precision that only works for Go. For the discover use case — generating stubs that humans review and refine — syntactic pattern matching is sufficient. The stubs don't need to be perfect; they need to be close enough to eliminate the blank-page problem.

### Framework Descriptions

Each supported framework is described by a **framework description file** — a declarative YAML file containing tree-sitter queries and metadata that teaches discover how to read that framework's source patterns. This is the core extensibility mechanism.

A framework description contains:

**Language and detection rules.** Which language this applies to, and how to auto-detect it from project files (e.g., presence of `go-chi/chi` in go.mod, `gem 'sinatra'` in Gemfile, `gem 'rails'` in Gemfile).

**Route queries.** Tree-sitter queries that match route registration patterns, with captures for HTTP method, path, and handler reference. For Chi: match method calls like `r.Post(path, handler)` on identifiers that look like router variables. For Rails: match DSL calls like `get '/path', to: 'controller#action'` and `resources :name` in routes.rb. For Sinatra: match `get '/path' do ... end` blocks on the app class.

**Middleware queries.** Tree-sitter queries that match middleware registration (e.g., `r.Use(middleware)` in Chi, `use Rack::Auth` in Ruby, `before_action` in Rails).

**Group/prefix queries.** Queries that match route grouping and path prefix nesting (e.g., `r.Route("/api", fn)` in Chi, `namespace :api do ... end` in Rails).

**Protocol boundary queries.** Queries that match calls to known database, HTTP client, cache, and message queue libraries. These are language-specific but framework-agnostic — ActiveRecord calls (`User.where(...)`, `User.create(...)`) are the same whether the app is Rails or Sinatra. Organized by protocol type (postgresql, mysql, redis, http, kafka) with captures for direction (read/write) and target (table name, URL, topic) where statically determinable.

**Grouping hints.** Rules for how to cluster discovered routes into blueprint-sized groups. For Go: group by package. For Rails: group by controller class. For Sinatra: group by file.

Framework descriptions are shipped as built-in files for supported frameworks and can also be provided by users for custom or internal frameworks. A user writes a YAML file with tree-sitter queries, drops it in `.linespec/frameworks/`, and discover picks it up. No Go code required.

**Limitation of the declarative approach:** Frameworks with heavily metaprogrammed or reflection-based routing (some Ruby DSLs, gRPC service registration, GraphQL schema-driven routing) may not be fully expressible as tree-sitter queries. For these, the framework description can declare `partial: true`, and discover will report discovered routes alongside a warning that coverage may be incomplete. A Go plugin interface is a possible future escape hatch but is deferred — the declarative approach covers the mainstream frameworks.

### Phase 1: Route Discovery

Parse the project's source files using tree-sitter with the detected (or specified) framework description. Execute the route queries, middleware queries, and group/prefix queries to build a full route table.

For each discovered route, produce a tuple of (method, full path, handler reference, middleware chain, source location). The handler reference is a file path + function/method name, not a resolved symbol — tree-sitter gives us the syntactic reference, which is sufficient for linking to `affected_scope` and generating spec stubs.

**Language-specific notes:**

- **Go (Chi):** Routes are typically scattered across multiple files, wired together in a main or router setup function. The group/prefix queries need to handle Chi's `r.Route("/prefix", func(r chi.Router) { ... })` pattern, which nests route registrations inside function literals.
- **Ruby (Rails):** Routes are centralized in `config/routes.rb` (or split across `config/routes/*.rb`). The DSL is highly regular: `get`, `post`, `put`, `patch`, `delete`, `resources`, `resource`, `namespace`, `scope`, `mount`. `resources :users` expands to seven conventional routes — the recognizer expands these to their individual endpoints.
- **Ruby (Sinatra):** Routes are method calls directly on the app class or in the top-level scope: `get '/path' do ... end`. Simpler than Rails but can be spread across multiple files via `require`.

### Phase 2: Protocol Boundary Tracing

For each discovered handler, locate its implementation in the source and run the framework description's protocol boundary queries against the handler's body and any functions it calls (bounded depth, default 3 levels of call chain).

This phase walks the call graph syntactically: find function/method definitions that match the handler reference, run boundary queries on their bodies, then follow any function calls to their definitions and repeat. Without semantic type resolution, the call graph walk relies on naming conventions and import patterns rather than resolved types. This is less precise than Go-native type resolution but works across all languages.

**Protocol boundary patterns by language:**

- **Go:** pgx/database-sql calls (`pool.Query`, `db.Exec`), net/http client calls (`http.Get`, `client.Do`), go-redis calls, sarama/franz-go producer/consumer calls.
- **Ruby:** ActiveRecord calls (`Model.where`, `Model.find`, `Model.create`, `Model.update`, `Model.destroy`, and raw SQL via `ActiveRecord::Base.connection.execute`). Net::HTTP / Faraday / HTTParty calls for external HTTP. Redis gem calls. Bunny/Sneakers for RabbitMQ. ruby-kafka for Kafka.

Each boundary hit yields (protocol, direction, target). For ActiveRecord, the model name maps to a table name via Rails naming conventions (pluralize + underscore). For raw SQL, extract the table name from the query string if it's a static literal.

**Dynamic calls:** Anything that can't be statically resolved (metaprogrammed method calls in Ruby, dynamically constructed queries, etc.) is flagged as "dynamic — manual classification needed" in the output rather than silently dropped.

### Phase 3: Spec Stub Generation

For each discovered route, combine the route metadata (method, path, middleware) with the traced protocol boundaries to generate a skeleton `.linespec` file:

- `RECEIVE` from the route's method and path
- `HEADERS` from middleware analysis (e.g., auth middleware implies `Authorization` header)
- `EXPECT` clauses from each protocol boundary hit
- `RESPOND` with a placeholder status code (200 for GET, 201 for POST, etc.)

These are stubs, not complete specs. The `EXPECT` clause bodies (query matchers, payload shapes) and `RESPOND` bodies are left as `# TODO` comments. The structure is the valuable part — it tells the engineer which protocol interactions exist at each endpoint and gives them a skeleton to fill in.

Stubs are written to a configurable specs directory (default: `specs/`), named by route: `specs/post_api_v1_shorten.linespec`.

### Phase 4: Record Generation

Group related routes into draft blueprint records using the framework description's grouping hints. For Go: group by package. For Rails: group by controller class. For Sinatra: group by file.

For each group, generate a draft blueprint record with:

- `title` — derived from the group identity (e.g., "UsersController endpoints", "URL Shortener Handlers")
- `status` — `draft`
- `type` — `blueprint`
- `affected_scope` — the file paths for all handlers, models, and migrations in the group
- `associated_specs` — paths to the generated `.linespec` stubs
- `intent` — empty by default; populated from git history with `--enrich`
- `constraints` — empty (these require human judgment)

Records are written to the provenance directory using the standard `prov-YYYY-XXXXXXXX` naming.

### Phase 5 (optional, `--enrich`): Git History Analysis

When `--enrich` is passed, run `git log --follow` for each file cluster in a blueprint's `affected_scope`. Collect the commit messages and, if available, PR titles (from merge commit conventions or GitHub/GitLab API if credentials are available).

Pass the collected messages through an LLM with a focused prompt: "Given these commit messages for files [list], write a 1-2 sentence intent summarizing what this code does and why it exists." The result populates the blueprint's `intent` field.

This is the only LLM-dependent step. Without `--enrich`, the entire pipeline is deterministic. The LLM is used for natural language synthesis, not structural analysis — all structural decisions are made by the tree-sitter phases.

---

## Shared Framework Descriptions and Sedum Integration

The framework description files are designed to be shared between `linespec provenance discover` and Sedum's architecture target plugins. Both tools need to understand "what does a Chi project (or a Rails project) look like in source code" — discover needs it to read existing code, and Sedum's world state scanner needs it to assess what's already been built before planning differential generation.

A single framework description file serves both tools:

- **Discover** uses the route queries, boundary queries, and grouping hints to scan an existing codebase and produce draft records + spec stubs.
- **Sedum's world state scanner** uses the same queries to derive world state predicates (`route_registered`, `model_exists`, `migration_exists`, etc.) from an existing codebase during `sedum graft`.

The parts that remain Sedum-specific are the convention descriptor (where to put generated files, naming transforms) and the primitive generators (how to produce code from templates). Those are about writing, not reading. But the framework description covers the reading half completely, which means adding a new framework to discover automatically gives Sedum the ability to scan existing projects of that framework.

**Contribution path:** Someone who wants LineSpec/Sedum support for their framework writes a framework description file (tree-sitter queries + metadata in YAML). That single contribution unlocks discover scanning, Sedum world state scanning, and eventually `sedum graft` for existing projects. The generation side (Sedum convention descriptors + primitive templates) can come as a separate, later contribution.

---

## Effort Estimate

### Phase 0: Tree-sitter Infrastructure — ~2 weeks

Integrate go-tree-sitter into the linespec codebase. Build the framework description loader and query execution engine: parse a YAML framework description, compile its tree-sitter queries, execute them against source files, and return structured results. Build the auto-detection logic (scan for go.mod, Gemfile, package.json, etc. and match against framework detection rules). This is the foundation everything else builds on.

### Phase 1: Route Discovery + First Framework Descriptions — ~2-3 weeks

Write the Chi and Rails/Sinatra framework descriptions (tree-sitter queries for routes, middleware, groups). Build the route table assembler that takes raw query matches and resolves them into a full route table with nested prefixes expanded. Rails `resources` expansion logic lives here. Test against real Chi and Rails projects.

### Phase 2: Protocol Boundary Tracing — ~2-3 weeks

Build the syntactic call graph walker. Write protocol boundary queries for Go (pgx, database/sql, net/http client) and Ruby (ActiveRecord, Net::HTTP/Faraday, Redis gem). The main complexity is the bounded call chain traversal and handling cross-file function resolution without full type information. ActiveRecord convention-based table name inference (model name → table name) lives here.

### Phase 3: Spec Stub Generation — ~1 week

Template generation from structured route + boundary data. The LineSpec DSL syntax is simple and well-defined. Same logic regardless of source language.

### Phase 4: Record Generation — ~1 week

Grouping heuristic (per framework description's hints) + YAML generation using existing provenance record creation infrastructure (`Commands.Create` and `Record` types already exist in `pkg/provenance/`).

### Phase 5: Git Enrichment — ~1 week

Git log parsing + LLM prompt construction. Optional and independent of the structural phases.

### Testing + Integration — ~2 weeks

Integration tests against known reference codebases — the url-shortener (Go/Chi) from the Sedum test suite and a representative Rails or Sinatra app. Edge case handling for dynamic queries, metaprogrammed routes, and unusual project layouts. Verify that framework descriptions produce identical output across runs (determinism check).

**Total: ~11-13 weeks for a working v1** with Go (Chi) and Ruby (Rails + Sinatra) support, PostgreSQL + HTTP boundary tracing for both languages, and optional git enrichment.

Each phase is independently testable and demoable:

- Phase 0 alone validates the tree-sitter integration and framework description format.
- Phase 0 + 1 gives "here are all your routes" — useful standalone for teams starting adoption.
- Phase 0 + 1 + 2 adds "and here's what each route talks to" — the protocol boundary map.
- Phase 0 + 1 + 2 + 3 produces spec stubs — the first real time savings for engineers.
- Phase 0 + 1 + 2 + 3 + 4 produces the full draft provenance graph — the complete onboarding experience.

Adding a new framework after v1 requires only a framework description file (YAML + tree-sitter queries) and protocol boundary queries for that language's common libraries — estimated at ~1-2 weeks per framework, not the full 11-13 week cycle.

---

## What This Doesn't Cover (Future Work)

- **External connectors** (GitHub/GitLab API, Jira, Asana, Slack) for richer intent mining — these are the Provisio commercial layer, not the open-source CLI.
- **Additional languages** — Python (Flask/Django/FastAPI), TypeScript (Express/Nest/Hono), Java (Spring). Each follows the same pattern: write a framework description + protocol boundary queries. ~1-2 weeks per framework.
- **Incremental/watch mode** — re-running discover on changed files. The full scan is the v1; incremental is a fast-follow.
- **Sedum convention descriptors + generators** — the framework description covers the shared read side. Sedum's write side (convention descriptors, primitive generators) for new frameworks remains a separate contribution, though informed by the same framework knowledge.
- **Non-HTTP entry points** — CLI commands, background workers, cron jobs, event consumers. The current design is route-centric. Extending to non-HTTP entry points means adding new query categories to the framework description format.

---
name: goray
description: Deterministic Go semantic code intelligence via go/packages and go/types. Use for all Go codebase navigation, architecture overview, symbol lookup, references, callers, interface implementations, call graphs, change impact, untested complexity hotspots, dead code, test/lint execution, and structured summary caching. Strictly takes precedence over semantic/vector search (cocoindex-code / ccc) and grep for any known Go symbol or Go structure when the goray MCP server is connected.
---

# goray

In OpenCode, tools are named `goray_<tool>` directly or live under
`tools.goray.*` in Code Mode.

## Tool routing & precedence (overrides `ccc` / `cocoindex-code` and `grep`)

When working in a Go repository alongside semantic/vector search:

1. **NEVER use `cocoindex-code.search` or vector search for a known Go identifier** (struct, interface, func, method, field, type, package, or module). Vector search is probabilistic and misses callers/implementations; `goray` is compiler-exact (`go/types`).
2. **NEVER run generic `search` to discover Go module structure or `goray` tool names.** Call `goray` tools directly from the list below.
3. **Only use `cocoindex-code.search` / `ccc` for:**
   - Non-Go files (`web/`, TypeScript/React, SQL, YAML, Dockerfiles, Markdown, configs).
   - Conceptual discovery in Go **only while you do not know any symbol name yet**. Switch to `goray` as soon as you have one.
4. **Only use `rg` / direct file read for:** literal strings, comments, struct tags, error text, or exact lines immediately before editing.

Anti-patterns:

- **WRONG:** `cocoindex-code.search(query="HTMLGraph module graph")`
  **RIGHT:** `lookup_symbol(query="HTMLGraph")` + `get_references(symbol="HTMLGraph")`
- **WRONG:** `cocoindex-code.search(query="CheckoutPort implementations")` or `rg "CheckoutPort"`
  **RIGHT:** `find_implementations(symbol="CheckoutPort")` + `get_impact_analysis(symbol="CheckoutPort")`
- **WRONG:** `search(query="goray project info codebase map")`
  **RIGHT:** Call `project_info`, `get_codebase_map`, and `get_dependency_graph` directly in parallel.

## Connection check

1. Call `project_info` (batch it with `get_codebase_map` for initial orientation).
2. If tools are missing, ask the user to run:

   ```sh
   make install
   opencode mcp add goray --global -- goray serve
   opencode mcp list
   ```

3. Check `index_state`:
   - `ready`: use the snapshot.
   - `dirty` / `rebuilding`: retry; responses may use the last good snapshot.
   - `error`: inspect `load_error_details`; production errors keep the last healthy snapshot. Test-only errors appear in `test_load_error_details`.
   - `initializing`: wait, then retry.

`project_info` also reports `last_build_mode` (`full` or `incremental`), `affected_packages`, `build_tags`, `workspace`, and `module_paths`.

## Tool catalog

Every registered MCP tool and its critical parameters:

| Tool | Parameters |
|---|---|
| `project_info` | none. Readiness, counts, load errors, build mode, DB paths, available tool count. |
| `get_codebase_map` | `format` (`markdown` default, plus registered formats), `detail` (`compact` default, `full`), `summary_only` (default `false`), `top_modules` (default `15`; `0` = all), `sort_by` (`prod_loc` default, `fan_in`, `fan_out`), `tracked_only` (bool). Compact scorecard reports generated file/symbol/LOC rollups, accurate `untracked_files`, cached module summaries (`summary`, `sync_state`, `deps_changed`), plus `truncated`, `total_count`, and a fetch hint. |
| `get_module_overview` | `module` (required: module name such as `payments` or `internal/index`, or `mod:` ID), `format`, `detail` (`compact` default, `full`), `include_generated` (default `false`), `tracked_only`. Compact returns package rollups, dependency edges, large files, top hotspots with `generated_omitted`, and the cached module summary when present. |
| `get_dependency_graph` | `package` (optional package path or module name; omit to rank fan-in/fan-out), `direction` (`out` default, `in`, `both`), `depth` (default `2`), `format` (default `json`). |
| `explain_dependency` | `from` (required module name or package path), `to` (required), `include_tests` (default `false`), `limit` (max usage sites). Returns exact `file:line` imports and symbol usages, blank/dot imports flagged, counters, and reverse-edge hint. |
| `lookup_symbol` | `symbol_id` (required: canonical ID, full Go identifier, repository-relative shorthand, `Type.Field`, exact name, or substring), `kind` (`func`, `method`, `interface`, `struct`, `type`, `var`, `const`, `field`), `limit` (default `25`). Returns `id` and `canonical_id`; includes `summary` or an `enclosing_summary` when the symbol itself is not eligible. |
| `get_api_surface` | `package` (required: relative package path or full import path), `include_generated` (default `false`), `limit` (default `100`; `0` = unlimited), `offset` (default `0`), `kind` (`interface`, `type`, `func`, `const`, `var`). Reports `generated_omitted`, `total_count`, `truncated`, and `next_offset`. |
| `get_references` | `symbol_id` (required), `limit` (default `100`). Type-checked references with `from_func_id` and `file:line`; struct fields resolve as `Type.Field`. |
| `get_call_graph` | `symbol_id` (required), `direction` (`callers`, `callees`, `both` default), `depth` (default `2`), `limit` (default `80`), `include_interface_dispatch` (default `true`). Dispatch edges carry `edge_kind: "interface_dispatch"` and `via_interface`. |
| `find_implementations` | `symbol_id` (required interface or concrete type). Compiler-exact `go/types.Implements`, including test mocks. |
| `get_impact_analysis` | `symbol_id` or `package`, `depth` (default `3`). Kind-aware blast radius: `implementations`, `test_mocks`, `direct_references`, `transitive_callers`, `affected_packages`, `test_packages_to_run`, `composite_literals`, `unkeyed_literals`. |
| `get_dead_code` | `package_prefix`, `include_exported` (default `false`), `include_generated` (default `false`), `module`, `exclude_modules`, `group_by` (`none` default, `module`, `package`), `limit` (default `100`). Test symbols are never reported. |
| `get_hotspots` | `target` (`func` default, `file`, `type`), `sort_by` (`risk` default, `complexity`, `loc`, `fan_in`), `untested_only`, `module`, `exclude_modules`, `min_complexity`, `min_loc`, `tracked_only`, `include_generated`, `limit` (max `100`). |
| `get_function_body` | `symbol_id` (required). Exact source with line numbers, LOC, cyclomatic complexity. |
| `list_stale_summaries` | `kind`, `module`, `package`, `limit`, plus stale filters. Excludes `synced`, `not_eligible`, and dead symbols by default. |
| `get_summary_context` | `symbol_id` (single) or `symbol_ids` (required array, max 25), `max_chars` (default `40000`). PREPARES RAW CONTEXT FOR WRITING/UPDATING SUMMARIES, not for reading cached summaries. Returns contexts, hashes, schema, `not_eligible` parent package/module IDs, and `truncated_ids`. |
| `upsert_summaries` | `items` (required array of `symbol_id`, `summary`, `expected_self_hash`, `expected_dep_hash`), `model_id`. Use hashes returned by `get_summary_context`. |
| `get_summary_coverage` | `module` (optional; accepts `mod:<name>`). Coverage totals and grouped counts. |
| `run_tests` | `packages` (default `./...`; string with whitespace-separated patterns or an array), `run` (optional `-run` regex). Runs `go test -race -count=1`, forwards server build tags, and rejects values starting with `-`. |
| `run_lint` | `packages` (default `./...`; string or array). Runs `golangci-lint run`, falling back to `go vet`; forwards server build tags and rejects values starting with `-`. |
| `reindex` | none. Explicit clean full rebuild. Use after branch switches; normal file edits use automatic incremental rebuilds. |

## Navigation & audit

- Architecture: `get_codebase_map`, `get_module_overview`, `get_dependency_graph`, `explain_dependency`.
- Find code: `lookup_symbol` (returns `id` and `canonical_id`).
- Understand calls: `get_references`, `get_call_graph`, `get_function_body`.
- Interfaces: `find_implementations`; call graphs include in-module interface dispatch by default.
- Change risk: `get_impact_analysis` (blast radius and affected test packages).
- Complexity & test gaps: `get_hotspots`.
- Unused code: `get_dead_code`.
- API surface: `get_api_surface`.

Audit defaults:

- Keep `tracked_only: true` in `get_codebase_map`, `get_module_overview`, and `get_hotspots` so untracked scratch files do not pollute product metrics; pass `false` only when explicitly auditing untracked files.
- `test_only` packages/modules have no production LOC and are gate/e2e suites, not dead production code. Do not report them as `0 loc` regressions.
- Before refactoring a Go symbol, run `lookup_symbol` -> `get_impact_analysis` (plus `find_implementations` for interfaces) before reading or editing files.

Use `canonical_id` for all summary tools. IDs look like:

```text
mod:internal/cache
pkg:github.com/acme/app/internal/cache
file:internal/cache/cache.go
sym:github.com/acme/app/internal/cache.(*Cache).Sync
```

## Summary workflow

The server never generates prose. The client generates summaries.

1. List candidates with `list_stale_summaries`:
   ```json
   {"kind":"func","limit":25}
   ```
2. Fetch context in batches with `get_summary_context` (max 25 IDs, default 40,000 character budget):
   ```json
   {"symbol_ids":["sym:...","sym:..."],"max_chars":40000}
   ```
   Responses include `contexts` and `truncated_ids`. Fetch truncated IDs later. The first requested context is always returned.
3. Build the summary using the exact `schema` returned for that symbol kind. Do not add fields.
   Function/method:
   ```json
   {
     "intent": "Why this exists and its architectural role.",
     "side_effects": ["db_write"],
     "invariants": ["Caller must hold mu"],
     "concurrency": "Optional lock or context semantics."
   }
   ```
   Type/file/package/module:
   ```json
   {
     "intent": "Architectural responsibility.",
     "key_invariants": ["State ownership or lifecycle rule."],
     "boundaries": ["SQLite", "network", "filesystem"]
   }
   ```
4. Upsert with `upsert_summaries` and the hashes from context:
   ```json
   {
     "items": [{
       "symbol_id": "sym:...",
       "summary": {"intent": "...", "side_effects": ["none"], "invariants": []},
       "expected_self_hash": "...",
       "expected_dep_hash": "..."
     }],
     "model_id": "your-model-id"
   }
   ```
5. Handle per-item statuses:
   - `updated`: stored.
   - `hash_mismatch`: source changed; re-fetch context and regenerate.
   - `validation_error`: fix the schema/fields.
   - `not_found`: symbol disappeared.
6. Check `get_summary_coverage` after batches.

## Staleness rules

- `synced`: exact content hash hit.
- `soft_stale`: code unchanged, dependencies or children changed. The previous summary is returned with `deps_changed` and `changed_dependencies`.
- `hard_stale`: self changed; use the previous summary only as a baseline.
- `missing`: never summarized.
- `not_eligible`: do not summarize. Use `source` from the context.

Generated files and `_test.go` symbols are never summary-eligible.

## Practical rules

- Do not edit `.go` files to store summaries.
- Do not invent canonical IDs; use `lookup_symbol.canonical_id` or `list_stale_summaries`.
- Prefer batched `get_summary_context` over repeated single calls.
- Prefer automatic incremental reindexing. Call `reindex` after a branch switch or when an explicit synchronous full rebuild is required.
- Verify with `run_tests` / `run_lint` instead of shell commands when available.
- Build tags and workspaces: `goray serve|index|export|db|diff` accept `--tags` / `GORAY_BUILD_TAGS` and `--workspace` for the active `go.work`.
- CI architecture gate: `goray diff [base-ref] [path] --fail-on cycles,dead_code,breaking_api,untested_hotspots`.
- Manage the split SQLite cache outside MCP (working-tree DBs live under `worktrees/`, content-addressed summaries are shared across `git worktree` checkouts under `summaries/`):
  ```sh
  goray index /path/to/project
  goray db info /path/to/project
  goray db path /path/to/project
  goray db path --summaries /path/to/project
  goray db rm /path/to/project              # removes only checkout working DB (keeps shared summaries)
  goray db rm --all /path/to/project        # removes working DB and shared summary DB
  goray db prune                            # removes orphaned worktree DBs
  ```
- Generate the interactive offline report with:
  ```sh
  goray export html -o report.html /path/to/project
  ```

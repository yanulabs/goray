---
name: goray
description: Use the goray MCP tools to navigate Go codebases, assess change impact, run tests/lint, and maintain the structured summary cache. Use when a Go repository has the goray MCP server connected.
---

# goray

Prefer these tools over ad-hoc grep/read when they are available. In OpenCode,
tools are named `goray_<tool>` directly or live under
`tools.goray.*` in Code Mode.

## Connection check

1. Call `project_info`.
2. If the tools are missing, ask the user to run:

   ```sh
   make install
   opencode mcp add goray -- goray .
   opencode mcp list
   ```

3. Check `index_state`:
   - `ready`: use the snapshot.
   - `dirty`/`rebuilding`: retry after the rebuild; responses may use the last
     good snapshot.
   - `error`: inspect `load_error_details`; production errors keep the last
     healthy snapshot. Test-only errors appear in `test_load_errors` and do not
     invalidate production symbols.
   - `initializing`: wait, then retry.

## Navigation

- Architecture: `get_codebase_map`, `get_module_overview`,
  `get_dependency_graph`.
- Find code: `lookup_symbol` (returns both `id` and canonical `canonical_id`).
- Understand calls: `get_references`, `get_call_graph`, `get_function_body`.
- Interfaces: `find_implementations`.
- Change risk: `get_impact_analysis` (also returns test packages).
- Unused code: `get_dead_code`.

Use `canonical_id` for all summary tools. IDs look like:

```text
mod:internal/cache
pkg:github.com/acme/app/internal/cache
file:internal/cache/cache.go
sym:github.com/acme/app/internal/cache.(*Cache).Sync
```

## Summary workflow

The server never generates prose. The client generates summaries.

1. List candidates:

   ```json
   {"kind":"func","limit":25}
   ```

   `list_stale_summaries` excludes `synced`, `not_eligible`, and dead symbols
   by default.

2. Fetch context in batches (max 25 IDs, default 40,000 character budget):

   ```json
   {"symbol_ids":["sym:...","sym:..."],"max_chars":40000}
   ```

   Responses include `contexts` and `truncated_ids`. Fetch the truncated IDs in
   a later call. The first requested context is always returned.

3. Build the summary using the exact `schema` returned for that symbol kind.
   Do not add fields.

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

4. Upsert with the hashes from context:

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
- `soft_stale`: code unchanged, dependencies or children changed. The previous
  summary is returned with `deps_changed` and `changed_dependencies`.
- `hard_stale`: self changed; use the previous summary only as a baseline.
- `missing`: never summarized.
- `not_eligible`: do not summarize. Use `source` from the context.

Generated files and `_test.go` symbols are never summary-eligible.

## Practical rules

- Do not edit `.go` files to store summaries.
- Do not invent canonical IDs; use `lookup_symbol.canonical_id` or
  `list_stale_summaries`.
- Prefer batched `get_summary_context` over repeated single calls.
- Prefer automatic reindexing. Call `reindex` after a branch switch or when you
  need an explicit synchronous rebuild.
- Use `run_tests`/`run_lint` for verification instead of shell commands when
  available.
- Inspect the cache outside MCP with:

  ```sh
  goray -db-info /path/to/project
  goray -db-path /path/to/project
  ```

- Generate the interactive offline report with:

  ```sh
  goray -export-html report.html /path/to/project
  ```

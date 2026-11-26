# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `goray version` now prints commit, build time, Go version, and platform metadata. It supports `--short` and `--json`; top-level `-V` is accepted alongside `-v` and `--version`.

## [0.2.0] - 2026-11-26

### Added

- `inspect_type` for compiler-exact struct fields, tags, embeddings, promoted members, method sets, constructors, type parameters, and implementations. Metadata is computed during indexing and works with `ReleaseArtifacts`.
- `find_call_path` for deterministic shortest call chains across direct, interface-dispatch, and function-value edges, with reverse-path hints and bounded depth/path search.
- `get_diagnostics` for structured list/parse/type diagnostics. Failed production builds keep serving the last healthy snapshot.
- `get_architecture_diff` for architecture diffs over MCP. It reuses the live working-tree snapshot, indexes only the base ref, and returns JSON identical to `goray diff --format json`.
- MCP `annotations` for every tool (read-only, destructive, idempotent, and open-world hints). The server exposes 25 tools.
- `get_references` pagination and test/module/package filters; `get_function_body` accepts `symbol_ids` batches and `include_doc`.
- Function-value call edges (`func_value`), package-level initializer call and composite-literal edges, and deterministic distinct IDs for multiple `init` functions.
- `get_dead_code` `ignore_test_refs` option to report production functions referenced only by tests.
- ETag/304 support for `/api/report`, with stable timestamps and abort-aware, deduplicated polling.

### Changed

- Workspace mode loads all `go.work` modules in one `go/packages` pass, preserving a single `go/types` universe. Watchers follow `go.work`, `go.work.sum`, and modules outside the workspace root.
- Single-module and workspace indexes use distinct cache identities. Custom `GOWORK` filenames, Windows cache paths, and Windows port fallback are handled correctly.
- Diff comparison and worktree handling moved to `internal/diff`, shared by the CLI and MCP.
- Compact `get_codebase_map` and `get_module_overview` include cached module summaries and accurate untracked totals; `get_summary_coverage` accepts `mod:` IDs.
- `run_tests` and `run_lint` accept multiple packages, reject flag-like patterns, and forward server build tags.
- Canonical type hashing memoizes shared type DAGs, report assembly uses one bulk summary query, test summaries reuse parsed test ASTs, and unchanged live-report polls return `304`. Summary prompt version is now `v2`.
- Split oversized index, diff, report, and MCP tool files into responsibility-focused modules.

### Fixed

- `goray serve` no longer deadlocks when the watcher is active and the client sends `shutdown` or closes stdin.
- MCP `notifications/cancelled` cancels the matching in-flight tool and suppresses its reply. Semaphore waits honor cancellation, marshal failures return `-32603`, and subprocess output is bounded during execution.
- Field references resolve for generic instantiations, cross-module workspaces, and incremental rebuilds. Interface dispatch and impact follow promoted methods to their declaring types and exclude unrelated methods.
- Failed incremental rebuilds retain pending changes, pathless `MarkDirty` forces a full rebuild, affected-package counts are exact, and test-only reverse importers are reloaded.
- Module cycle enumeration reports all elementary cycles. Non-function impact walks transitive callers, not-found errors return nearest matches, file/type hotspots exclude tests, tracked views recompute metrics and edges, and test metrics honor build tags.
- `breaking_api` ignores `internal/` and `cmd/`, includes exported fields, and compares full interface signatures. `untested_hotspots` only fails on regressions, new hotspots, or lost coverage. `goray diff` accepts a single path positional, symlinked roots, and releases build artifacts.
- Fixed Windows `WSAEADDRINUSE` port fallback and RFC 8089 SQLite URIs, workspace/single-module database collisions, custom `GOWORK` handling, and Antigravity `MCP_CWD` config generation. `make db-info` invokes `db info`.
- `get_summary_context` returns the structured cold-start payload, `get_summary_coverage` normalizes `mod:`, `get_api_surface` paginates before enrichment, and duplicate summary lookups are removed.
- Modified breaking-API entries include both the previous and new signatures in JSON and Markdown output.

## [0.1.4] - 2026-11-24

### Changed

- Production and test packages now share one typed package-loading pass, reducing duplicate type checking, startup allocations, and peak memory while preserving type identity across test references and interface dispatch.
- Canonical signature hashing streams directly into SHA-256 instead of building large temporary strings, without changing summary hashes.
- `goray serve` releases AST and `go/types` artifacts and returns unused build memory to the OS after indexing by default, cutting retained heap on large repositories while keeping queries on the compact derived index. Use `--retain-packages` to opt back into memory-heavy incremental rebuilds.

## [0.1.3] - 2026-11-22

### Fixed

- Indexing now loads typed syntax only for project packages instead of parsing and retaining every transitive dependency; peak memory on a small module dropped from 1.9GB to 155MB.
- The report server caches snapshots and no longer rewrites all working rows on every HTTP poll.
- Incremental rebuilds load all affected packages in one `go/packages` call and honor cancellation.
- `goray diff` reports the real HEAD commit alongside the resolved base commit.
- Cache reads propagate iteration errors, and repeated `Cache.Close` calls retain the first failure.
- Git and browser subprocesses have bounded timeouts, and command tools wait for inherited output pipes to close.

### Changed

- CI verifies the Go version declared in `go.mod`.
- The report TypeScript contract is guarded by a Go field snapshot test.

## [0.1.2] - 2026-11-22

### Security

- Release builds now use Go 1.27.4, and the module requires Go 1.26.11 or newer. The v0.1.1 binaries were built with Go 1.26.0 and contain 46 standard-library vulnerabilities, including GO-2026-6859 in `net/http`.
- CI now runs `govulncheck` on every change.

### Fixed

- The filesystem watcher returns `WalkDir` failures instead of silently skipping unreadable directories.
- `goray diff` computes the base hotspot map once instead of once per head hotspot.
- The MCP server returns stdout write failures instead of silently continuing after a broken pipe.
- `run_tests` and `run_lint` distinguish timeouts and cancellations from exit failures and truncate output on UTF-8 boundaries.
- Sentinel and typed errors are handled through `errors.Is` and `errors.As`; missing state switch cases are explicit.
- Goray-owned cache directories use `0700` and port files use `0600`.

### Changed

- Split the cache and index summary implementations into focused files and reduced the highest-complexity impact, dependency, and module-context functions.
- Added coverage floors, cross-platform builds, embedded web artifact drift checks, and a pinned `golangci-lint` configuration.
- Added exported API documentation and aligned the documented Go and Node requirements.

## [0.1.1] - 2026-11-22

### Fixed

- `get_summary_context` accepts canonical `mod:` IDs for multi-package and single-package modules in single and batch modes, returns the expected module schema and hashes, and includes compact package rollups, dependency edges, exported API, key file excerpts, and cached child package summaries.
- `upsert_summaries` overwrites the stored payload, `model_id`, and timestamp when the same `(content_hash, prompt_version)` is written again, reports `updated` only when a row was actually replaced, and invalidates pending cache touch state.

## [0.1.0] - 2026-11-22

Initial public release.

### Added

- Deterministic Go semantic index built with `go/packages` and `go/types`, including packages, symbols, fields, imports, call edges, interface implementations, references, composite literals, dead code, complexity hotspots, test reachability, and module-level dependency rollups.
- MCP stdio server with 21 tools for architecture maps, module overviews, dependency explanations, symbol lookup, references, call graphs, interface implementations, impact analysis, dead code, hotspots, function bodies, API surfaces, summary caching, and verification commands.
- Compact and paginated MCP projections for high-volume tools, generated-file detection with generated symbol/LOC rollups, `generated_omitted` counters, and explicit payload-truncation metadata.
- Canonical `symbol_id`, `package`, and `module` parameters with backwards-compatible aliases.
- Tolerant symbol resolution for canonical IDs, full Go import paths, repository-relative shorthands, method/field shorthands, and canonical IDs without pointer stars.
- SQLite summary cache with split working-tree and shared summary databases, content/dependency hashing, staleness classification, batched context generation, optimistic upsert checks, coverage reporting, and worktree pruning.
- CLI commands: `serve`, `export` (HTML, Markdown, DOT), `index`, `diff` with CI gates, `db` inspection/removal/pruning, `version`, and help.
- Offline single-file HTML report and a React/TypeScript report source under `web/`.
- Incremental reindexing through filesystem watching and affected-package reloads.
- Interactive `make install-skill` and `make install-mcp` targets covering OpenCode, Claude Code, Codex, Cursor, Gemini CLI, Antigravity, and shared `~/.agents` configuration.
- Cross-platform release builds for Linux, macOS, and Windows on amd64/arm64, with SHA-256 checksums.
- CI verification, GoReleaser validation, CodeQL analysis, Dependabot updates, issue forms, pull request guidance, security policy, and contributor documentation.

### Fixed

- Single-package modules are eligible for summaries.
- Alias fields and test references no longer create false module cycles.
- Large report graphs no longer crash ELK after post-compaction.
- Nil method packages no longer panic interface-dispatch registration.

[Unreleased]: https://github.com/yanulabs/goray/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/yanulabs/goray/releases/tag/v0.2.0
[0.1.4]: https://github.com/yanulabs/goray/releases/tag/v0.1.4
[0.1.3]: https://github.com/yanulabs/goray/releases/tag/v0.1.3
[0.1.2]: https://github.com/yanulabs/goray/releases/tag/v0.1.2
[0.1.1]: https://github.com/yanulabs/goray/releases/tag/v0.1.1
[0.1.0]: https://github.com/yanulabs/goray/releases/tag/v0.1.0

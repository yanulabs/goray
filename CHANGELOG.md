# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/yanulabs/goray/compare/v0.1.4...HEAD
[0.1.4]: https://github.com/yanulabs/goray/releases/tag/v0.1.4
[0.1.3]: https://github.com/yanulabs/goray/releases/tag/v0.1.3
[0.1.2]: https://github.com/yanulabs/goray/releases/tag/v0.1.2
[0.1.1]: https://github.com/yanulabs/goray/releases/tag/v0.1.1
[0.1.0]: https://github.com/yanulabs/goray/releases/tag/v0.1.0

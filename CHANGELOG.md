# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/yanulabs/goray/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/yanulabs/goray/releases/tag/v0.1.0

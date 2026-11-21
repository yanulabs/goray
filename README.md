# go-mcp-server

An MCP stdio server that builds a semantic index of a Go module with
`go/packages` and `go/types`, then exposes architecture, symbol, call-graph,
impact-analysis, and cached-summary tools to an LLM client.

The server never calls an LLM. The connected MCP client produces summaries and
submits them with `upsert_summaries`; the server validates, hashes, and caches
them deterministically in SQLite.

## Build and run

```sh
go build -o go-mcp-server ./cmd/go-mcp-server
./go-mcp-server /path/to/go/project
```

Example MCP client configuration:

```json
{
  "mcpServers": {
    "go-mcp-server": {
      "command": "/absolute/path/to/go-mcp-server",
      "args": ["/absolute/path/to/go/project"]
    }
  }
}
```

The index builds in the background. File changes are watched automatically and
coalesced into debounced rebuilds. Queries wait briefly for an active rebuild;
otherwise they use the last good snapshot and report `index_state`.

### CLI flags

| Flag | Meaning |
|---|---|
| `-export-dot <file>` | Write the module graph as Graphviz DOT and exit |
| `-export-md <file>` | Write the architecture map as Markdown and exit |
| `-db <path>` | SQLite path; `:memory:` for ephemeral runs (or `GO_MCP_DB`) |
| `-no-watch` | Disable automatic file watching/reindexing |
| `-log-level off\|error\|info` | stderr log level (default `info`, or `GO_MCP_LOG_LEVEL`) |

The default database lives outside the repository at
`<UserCacheDir>/go-mcp-server/<sha256(absProjectPath)[:16]>.db`.

Module-level metrics use `modules/<name>` when present; otherwise standard
layout directories (`cmd/<name>`, `internal/<name>`, `pkg/<name>`) or the
first path segment form the module group. Root packages are grouped as `root`.

## Summary cache

- Canonical, line-independent IDs: `mod:`, `pkg:`, `file:`, and `sym:`.
- Normalized AST plus resolved `go/types` signatures and directives.
- Cycle-safe `sig_hash`/`self_hash`/`dep_hash`/`content_hash` model.
- Two-tier staleness: `synced`, `soft_stale`, `hard_stale`, `missing`, and
  `not_eligible`.
- Content-addressed summaries survive branch switches and function moves.
- Structured JSON validation for `func`/`method` and architecture kinds.
- LRU access tracking with batched flushes and periodic garbage collection.

## Tools

| Tool | Purpose |
|---|---|
| `project_info` | Index status, module path, counts, load errors |
| `get_codebase_map` | Module scorecard, cross-module graph, coupling, large files (Markdown/JSON/DOT/Mermaid) |
| `get_module_overview` | One module's packages, deps, large files, exported API (Markdown/JSON) |
| `get_dependency_graph` | Package BFS graph, fan-in/fan-out ranking (JSON/Mermaid/DOT) |
| `lookup_symbol` | Exact/fuzzy symbol lookup with summary enrichment |
| `get_api_surface` | Exported symbols with summary enrichment |
| `get_references` | Type-checked references with caller and `file:line` |
| `get_call_graph` | BFS callers/callees |
| `find_implementations` | `go/types.Implements` interface/type matrix |
| `get_impact_analysis` | Blast radius and test packages to run |
| `get_dead_code` | Unreferenced functions/methods |
| `get_function_body` | Source, LOC, and cyclomatic complexity |
| `list_stale_summaries` | Eligible symbols needing summaries, ordered bottom-up |
| `get_summary_context` | Schema, hashes, staleness, source, and dependency context |
| `upsert_summaries` | Validate and store summaries with optimistic hash checks |
| `get_summary_coverage` | Coverage counts by kind and module |
| `run_tests` | `go test -race -count=1` |
| `run_lint` | `golangci-lint run`, falling back to `go vet` |
| `reindex` | Rebuild the index and sync working state explicitly |

## Layout

```
cmd/go-mcp-server     CLI wiring
internal/index        semantic index, hashing, summary metadata, snapshot store
internal/cache        SQLite schema, working-state sync, summaries, staleness, GC
internal/watch        fsnotify-based project watcher
internal/mcp          JSON-RPC 2.0 stdio transport and tool protocol
internal/tools        tool registry and handlers
internal/render       renderer registry: Markdown, JSON, Mermaid, DOT
```

## Development

```sh
go test -race ./...
go vet ./...
golangci-lint run ./...
```

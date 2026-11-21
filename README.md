# go-mcp-server

An MCP stdio server that builds a semantic index of a Go module with
`go/packages` and `go/types`, then exposes architecture, symbol, call-graph,
and impact-analysis tools to an LLM client.

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

The index is built in the background at startup. Call `reindex` after changing
code if you want a fresh snapshot; queries use the last successful snapshot.

### CLI flags

| Flag | Meaning |
|---|---|
| `-export-dot <file>` | Write the module graph as Graphviz DOT and exit |
| `-export-md <file>` | Write the architecture map as Markdown and exit |
| `-log-level off\|error\|info` | stderr log level (default `info`, or `GO_MCP_LOG_LEVEL`) |

Module-level metrics use `modules/<name>` when present; otherwise standard
layout directories (`cmd/<name>`, `internal/<name>`, `pkg/<name>`) or the
first path segment form the module group. Root packages are grouped as `root`.

## Tools

| Tool | Purpose |
|---|---|
| `project_info` | Index status, module path, counts, load errors |
| `get_codebase_map` | Module scorecard, cross-module graph, coupling, large files (Markdown/JSON/DOT/Mermaid) |
| `get_module_overview` | One module's packages, deps, large files, exported API (Markdown/JSON) |
| `get_dependency_graph` | Package BFS graph, fan-in/fan-out ranking (JSON/Mermaid/DOT) |
| `lookup_symbol` | Exact/fuzzy symbol lookup |
| `get_api_surface` | Exported symbols of a package or module root |
| `get_references` | Type-checked references with caller and `file:line` |
| `get_call_graph` | BFS callers/callees |
| `find_implementations` | `go/types.Implements` interface/type matrix |
| `get_impact_analysis` | Blast radius and test packages to run |
| `get_dead_code` | Unreferenced functions/methods |
| `get_function_body` | Source, LOC, and cyclomatic complexity |
| `run_tests` | `go test -race -count=1` |
| `run_lint` | `golangci-lint run`, falling back to `go vet` |
| `reindex` | Rebuild the index from disk |

## Layout

```
cmd/go-mcp-server     CLI wiring
internal/index        semantic index, queries, graphs, impact, snapshot store
internal/mcp          JSON-RPC 2.0 stdio transport and tool protocol
internal/tools        tool registry and handlers
internal/render       renderer registry: Markdown, JSON, Mermaid, DOT
```

## Development

```sh
go test ./...
go vet ./...
golangci-lint run ./...
```

# goray

Static semantic indexer, MCP stdio server, and offline architecture reporter for Go modules.

`goray` loads a Go module with `go/packages` and `go/types` to compute dependency graphs, call graphs, interface implementations, dead code, complexity hotspots, and blast radius. It ships as a single pure-Go binary (no CGO) and exposes the index either as a self-contained HTML/Markdown/DOT export or over MCP for coding agents.

`goray` never calls an LLM. If a connected MCP client submits structured symbol summaries via `upsert_summaries`, `goray` validates, hashes, and caches them in an external SQLite summary database that can be shared across worktrees. Every command and report works with an empty cache.

## Install

```sh
go install github.com/yanulabs/goray/cmd/goray@latest

```

From a local checkout:

```sh
make install          # installs to GOBIN (or GOPATH/bin) with git version
make build            # builds ./bin/goray
make install-skill    # optional: installs the OpenCode skill to ~/.config/opencode/skills

```

## Quick start

Generate and open an offline architecture report for the current module:

```sh
goray export html --open

```

Connect `goray` to a Claude/Cursor-style MCP client:

```json
{
  "mcpServers": {
    "goray": {
      "command": "goray",
      "args": ["serve", "/absolute/path/to/go/project"]
    }
  }
}

```

With OpenCode (project-local):

```sh
opencode mcp add goray -- goray serve .

```

Install globally into OpenCode with the Makefile target:

```sh
make install-mcp
# or explicitly:
opencode mcp add goray --global -- goray serve
```

OpenCode V2 reads servers from `mcp.servers` (not directly under `mcp`). The generated configuration looks like:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "goray": {
        "type": "local",
        "command": ["goray", "serve"]
      }
    }
  }
}
```

The local server's `cwd` defaults to the OpenCode workspace. Set `"cwd": "/path/to/repo"` only if you want a fixed repository.

The repository also includes `.opencode/skills/goray/SKILL.md`, which OpenCode discovers automatically (or loads via `@goray`).

## CLI

```text
goray <command> [flags] [path]

```

`[path]` defaults to `.` and is resolved upward to the nearest directory containing `go.mod` (file paths inside the module work as well). Commands can be run from any subdirectory, and nested Go modules resolve to their own root. Flags may appear before or after `[path]`.

| Command | Description |
| --- | --- |
| `goray serve [path]` | Run the MCP stdio server |
| `goray export <html|md|dot> [path]` | Export an HTML report, Markdown map, or Graphviz DOT graph |
| `goray db <info|path|rm|prune> [path]` | Inspect, locate, remove, or prune SQLite databases |
| `goray index [path]` | Build and sync the semantic index once |
| `goray version` | Print version (`-v` / `--version` also accepted) |
| `goray help [command]` | Show help for root or a specific command |

### `serve`

```sh
goray serve [path] [--db <path>] [--log-level off|error|info] [--port <n>] [--open] [--no-watch]

```

* `--db <path>` — single-database override (default: split working and shared summary databases under the user cache dir; env `GORAY_DB`; supports `:memory:`).
* `--log-level <level>` — stderr verbosity: `off`, `error`, or `info` (default: `info`; env `GORAY_LOG_LEVEL`).
* `--port <n>` — local report UI port (default: `4321`; `0` picks a free port). Busy ports auto-increment.
* `--open` — open the local report URL in the default browser.
* `--no-watch` — disable `fsnotify` watching and background reindexing.

`serve` also runs a local read-only report UI and prints a Vite-style banner to **stderr**, keeping stdout reserved for MCP JSON-RPC:

```text
  goray dev

  ➜  Local:   http://127.0.0.1:4321/
  ➜  MCP:     stdio
  ➜  Press Ctrl+C to stop
```

The report reflects the current index snapshot and summary cache, and refreshes on each page load after rebuilds. The initial index builds in the background. File edits trigger debounced rebuilds; incoming queries wait briefly for an in-flight rebuild or serve the last healthy snapshot while reporting `index_state`. Parse or type errors in production code never overwrite the last good snapshot; test-only compile errors are surfaced as warnings.

### `export`

```sh
goray export <html|md|dot> [path] [-o <file>] [--db <path>] [--open]

```

* `-o, --out <file>` — write to a file instead of `stdout`.
* `--db <path>` — SQLite cache path used to enrich `html`/`md` exports with cached summaries.
* `--open` — open the generated report in the default browser (`html` only). Without `-o`, writes to `$TMPDIR/goray-<repo-slug>-report.html` and prints the path to `stderr`.

```sh
goray export html --open
goray export html -o report.html /path/to/repo
goray export md -o ARCHITECTURE.md
goray export dot | dot -T svg > deps.svg

```

The HTML export is a single offline file with all CSS, JS, and indexed metadata embedded (no external assets, no source dumps). It includes overview metrics, a layered dependency graph with cycle/back-edge detection, a sortable complexity/fan-in hotspots table, an interface implementation matrix, a hierarchy explorer, and a symbol inspector with upstream blast radius.

### `db`

```sh
goray db path [path] [--db <path>] [--summaries]
goray db info [path] [--db <path>]
goray db rm [path] [--summaries] [--all] [--dry-run] [--force] [--db <path>]
goray db prune [--dry-run] [--force]
```

`db path` prints only the resolved working database path to `stdout` without
indexing or creating the file:

```sh
sqlite3 "$(goray db path)"
```

`db path --summaries` prints only the shared summary database path. `db info`
prints JSON statistics and includes both `db_path` and `summary_db_path`.

`db rm` is safe by default:

```sh
goray db rm                 # current checkout's working DB + -wal/-shm only; summaries untouched
goray db rm --summaries     # shared summary DB only; working DB untouched
goray db rm --all           # both DBs and all sidecars
goray db rm --dry-run       # print exact files and byte sizes, delete nothing
goray db rm --db /path/x.db # explicit single DB; --db :memory: fails with exit code 2
```

Removal is idempotent (a missing DB prints `nothing to remove` and exits 0).
Before deleting, `db rm` acquires an exclusive SQLite lock; if another process
(such as `goray serve`) holds the DB it fails with exit code 1 unless `--force`
is passed. An explicit `--db` / `GORAY_DB` target is treated as a single DB,
including its `-wal` and `-shm` sidecars.

`db prune` scans `<user-cache>/goray/worktrees/*.db`, reads the `canonical_root`
persisted during `Sync`, and removes only databases whose checkout directory no
longer exists. It never touches `summaries/`, and skips locked or unannotated
databases while reporting them in the output.

### `index`

```sh
goray index [path] [--db <path>] [--json]
```

Builds the semantic index exactly once and syncs `working_symbols` and
`working_edges` to SQLite. It does not start fsnotify, the MCP stdio server, or
the local report UI.

```sh
goray index
goray index /path/to/repo --json
goray index --db :memory: .
```

`--json` emits a single object with `root`, `module_path`, `db_path`,
`summary_db_path`, `packages`, `files`, `symbols`, `edges`, `eligible`,
`synced`, `coverage_percent`, `warnings`, and `duration_ms`.

Production parse/type errors exit `1` without overwriting an existing healthy
working snapshot. Errors confined to `*_test.go` files sync production state,
print warnings to stderr, and exit `0`.

## Storage and caching

By default, storage is split into two SQLite databases outside the repository so
nothing needs to be gitignored:

```text
<os.UserCacheDir()>/goray/worktrees/<dir-slug>-<sha256(canonicalRoot)[:12]>.db
<os.UserCacheDir()>/goray/summaries/<mod-slug>-<sha256(modulePath)[:12]>.db
```

The **working database** is checkout-local and ephemeral. It stores only
`working_symbols`, `working_edges`, and checkout schema metadata. Symlinks are
resolved before hashing the canonical root, so every `git worktree` checkout
and moved directory gets an isolated snapshot.

The **summary database** is content-addressed and shared. It stores only
`summary_cache`, `symbol_history`, and summary schema metadata. `<mod-slug>` is
derived from the last segment of the module path in `go.mod` (lowercase
`a-z0-9._-`, max 32 chars, fallback `module`). Worktrees and clones of the same
module resolve to the same file, so a summary cached for a content hash is
immediately reusable across branches.

`--db` and `GORAY_DB` (including `:memory:`) switch to single-database mode:
all tables live in that one database and no shared summary file is created.

* **Concurrency:** the summary database is attached per SQLite connection with
  WAL and a 5000 ms busy timeout. Working-state syncs never write summary
  tables, and summary upserts/GC/LRU flushes never write working tables; no
  transaction spans both databases.
* **Canonical IDs:** line-independent identifiers (`mod:`, `pkg:`, `file:`,
  `sym:`) so cached entries survive line shifts, file moves, and branch switches.
* **Semantic hashing:** normalized AST combined with resolved `go/types`
  signatures and compiler directives, using a cycle-safe `sig_hash` /
  `self_hash` / `dep_hash` / `content_hash` chain.
* **Staleness states:** `synced`, `soft_stale` (dependencies changed),
  `hard_stale` (symbol changed), `missing`, and `not_eligible`. Direct lookup
  always checks `(content_hash, prompt_version)` before consulting
  `symbol_history`, so both branches of a worktree pair stay synced
  independently.
* **Eligibility:** generated files and `*_test.go` symbols remain indexed for
  navigation and impact analysis, but are excluded from summary eligibility and
  production risk hotspots.
* **Module grouping:** uses `modules/<name>` when present; otherwise
  `cmd/<name>`, `internal/<name>`, `pkg/<name>`, or the first path segment
  (`root` for top-level packages).
* **Maintenance:** batched LRU access tracking with periodic garbage collection
  and optimistic hash checks on write.

## MCP tools

### Architecture and dependencies

| Tool | Purpose |
| --- | --- |
| `project_info` | Index state, module path, working and summary DB paths, symbol counts, load errors |
| `get_codebase_map` | Concise module scorecard, cross-module edges and cycles, large files, top hotspots; `tracked_only` (`md`/`json`/`dot`/`mermaid`) |
| `get_module_overview` | Single module packages, dependencies, large files, exported API, `tracked_only` (`md`/`json`) |
| `get_dependency_graph` | Package BFS dependency graph and fan-in/fan-out ranking (`json`/`mermaid`/`dot`) |

### Symbols, calls, and impact

| Tool | Purpose |
| --- | --- |
| `lookup_symbol` | Exact or fuzzy symbol lookup with cached summary metadata |
| `get_api_surface` | Exported symbols with cached summary metadata |
| `get_references` | Type-checked references with enclosing caller and `file:line` |
| `get_call_graph` | BFS callers and callees |
| `find_implementations` | `go/types.Implements` interface-to-type matrix |
| `get_impact_analysis` | Transitive blast radius and affected test packages |
| `get_dead_code` | Unreferenced functions/methods with closed-world, interface, generated, module, and grouped filters |
| `get_hotspots` | Rank funcs/files/types by risk, complexity, LOC, or fan-in; static test reachability and module/generated/untracked filters |
| `get_function_body` | Source body, LOC, and cyclomatic complexity |

### Summary cache

| Tool | Purpose |
| --- | --- |
| `list_stale_summaries` | Eligible symbols missing or stale, ordered bottom-up |
| `get_summary_context` | Schema, hashes, staleness, source, and dependency context (batched up to 25) |
| `upsert_summaries` | Validate and store structured JSON summaries with hash conflict checks |
| `get_summary_coverage` | Coverage breakdown by kind and module |

### Verification and control

| Tool | Purpose |
| --- | --- |
| `run_tests` | Run `go test -race -count=1` |
| `run_lint` | Run `golangci-lint run`, falling back to `go vet` |
| `reindex` | Force a full index rebuild and working-state sync |

## Layout

```text
cmd/goray                CLI entrypoint, subcommand routing, versioning
internal/index           go/packages + go/types indexer, hashing, eligibility
internal/cache           SQLite schema, working state, summary store, GC
internal/watch           fsnotify watcher with debounced rebuilds
internal/mcp             JSON-RPC 2.0 stdio transport
internal/tools           MCP tool registry and handlers
internal/report          HTML report view-model builder
internal/render          Markdown/DOT/HTML renderers and embedded web assets
.opencode/skills/goray   OpenCode skill definition

```

## Development

```sh
make verify   # gofmt check, go vet, go test -race -count=1, golangci-lint

```

## License

MIT
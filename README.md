# goray

[![CI](https://github.com/yanulabs/goray/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/yanulabs/goray/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/yanulabs/goray?sort=semver)](https://github.com/yanulabs/goray/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/yanulabs/goray.svg)](https://pkg.go.dev/github.com/yanulabs/goray)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Static semantic indexer, MCP stdio server, and offline architecture reporter for Go modules.

> **Status:** pre-1.0. The CLI and MCP schemas are usable, but minor releases may add or adjust behavior. Pin a release when integrating into production automation.

`goray` loads a Go module with `go/packages` and `go/types` to compute dependency graphs, call graphs, interface implementations, dead code, complexity hotspots, and blast radius. It ships as a single pure-Go binary (no CGO) and exposes the index either as a self-contained HTML/Markdown/DOT export or over MCP for coding agents.

`goray` never calls an LLM. If a connected MCP client submits structured symbol summaries via `upsert_summaries`, `goray` validates, hashes, and caches them in an external SQLite summary database that can be shared across worktrees. Every command and report works with an empty cache.

## Install

### Go toolchain

```sh
# Latest release
go install github.com/yanulabs/goray/cmd/goray@latest

# Pin a version
go install github.com/yanulabs/goray/cmd/goray@v0.1.3

goray version
```

Building from source requires Go 1.26.11 or newer. `goray` is pure Go with `CGO_ENABLED=0`; installed builds report their module version through `goray version`.

### Prebuilt binaries

Download Linux, macOS, or Windows archives (amd64/arm64) from
[GitHub Releases](https://github.com/yanulabs/goray/releases). Each release
includes `checksums.txt` with SHA-256 digests.

### From a local checkout

```sh
make install          # installs to GOBIN (or GOPATH/bin) with git version
make build            # builds ./bin/goray
make install-skill    # installs the agent skill; prompts for target(s)
make install-mcp      # installs the binary and registers the MCP server; prompts
```

`make install-mcp` and `make install-skill` accept the same selection syntax:
target numbers or names, comma/space-separated lists, `detected`, `installed`,
`all`, or an explicit path. The MCP installer detects OpenCode, Claude Code,
Codex, Cursor, Gemini CLI, Antigravity, and the shared `~/.agents` directory.

## Quick start

Generate and open an offline architecture report for the current module:

```sh
goray export html --open

```

## MCP client setup

`make install-mcp` installs the binary, discovers installed clients, and prompts
for one or more targets. `make uninstall-mcp` removes the `goray` entry using the
same selection syntax.

```sh
make install-mcp                                  # interactive target picker
make install-mcp MCP_TARGET=claude                # one client
make install-mcp MCP_TARGET=opencode,cursor       # several clients
make install-mcp MCP_TARGET=antigravity           # Antigravity App/IDE
make install-mcp MCP_TARGET=all                   # every supported client
make install-mcp MCP_TARGET=detected              # only detected clients
make install-mcp MCP_SCOPE=project                # project-local configs in $PWD
make install-mcp MCP_ARGS='serve /absolute/path/to/repo'  # pin a repository
make install-mcp MCP_CONFIG=/tmp/mcp.json         # explicit file, no prompt
```

Supported targets:

| Target | Global config | Project config | Entry format |
| --- | --- | --- | --- |
| `opencode` | `~/.config/opencode/opencode.json` | `./opencode.json` | `mcp.servers.<name>` |
| `claude` | `~/.claude.json` | `./.mcp.json` | `mcpServers.<name>` |
| `codex` | `~/.codex/config.toml` | `./.codex/config.toml` | `[mcp_servers.<name>]` |
| `cursor` | `~/.cursor/mcp.json` | `./.cursor/mcp.json` | `mcpServers.<name>` |
| `gemini` | `~/.gemini/settings.json` | `./.gemini/settings.json` | `mcpServers.<name>` |
| `antigravity` | `~/.gemini/config/mcp_config.json` | `./.agents/mcp_config.json` | `mcpServers.<name>` |
| `agents` | `~/.agents/mcp.json` | `./.agents/mcp.json` | `mcpServers.<name>` |

`MCP_NAME` defaults to `goray` and `MCP_ARGS` defaults to `serve`; names are
restricted to letters, digits, `_`, and `-`. `MCP_FORMAT` overrides format
inference for `MCP_CONFIG` (`opencode`, `claude`, `codex`, `cursor`, `gemini`,
`antigravity`, `agents`, or `generic`). `OPENCODE_CONFIG` remains available as an
OpenCode-only override. `CLAUDE_CONFIG_DIR` and `CODEX_HOME` are honored.
Project-scoped Codex configs load only for trusted projects.

Antigravity App/IDE never reads Gemini CLI's `~/.gemini/settings.json`; it reads
`~/.gemini/config/mcp_config.json` globally and `.agents/mcp_config.json` per
workspace. Antigravity also spawns MCP servers with cwd `/`, so the installer
writes `args: ["serve", "${workspaceFolder}"]` for `goray`. If your Antigravity
build passes `${workspaceFolder}` through literally, pin the repo with
`MCP_CWD=/absolute/path/to/repo`; that writes a `cwd` entry. Restart Antigravity
or use **Customizations → Installed → MCP Servers → Restart** after installing.

The installer edits JSON/TOML files with `python3` and follows symlinked config
files. It refuses to rewrite JSONC files; use the client's own command for those.
When stdin is not a terminal and no target is supplied, `make install-mcp`
defaults to `opencode` for backwards compatibility.

Equivalent per-client commands:

```sh
opencode mcp add goray -- goray serve .
claude mcp add --scope user goray -- goray serve
codex mcp add goray -- goray serve
gemini mcp add --scope user goray goray serve
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
| `goray diff [base-ref] [path]` | Deterministic architectural delta vs a git ref, with CI gates |
| `goray version` | Print version (`-v` / `--version` also accepted) |
| `goray help [command]` | Show help for root or a specific command |

### `serve`

```sh
goray serve [path] [--db <path>] [--log-level off|error|info] \
  [--port <n>] [--port-range <n>] [--strict-port] [--host <addr>] \
  [--port-file <path>] [--tags <list>] [--workspace] [--open] [--no-watch]

```

* `--db <path>` — single-database override (default: split working and shared summary databases under the user cache dir; env `GORAY_DB`; supports `:memory:`).
* `--log-level <level>` — stderr verbosity: `off`, `error`, or `info` (default: `info`; env `GORAY_LOG_LEVEL`).
* `--port <n>` — preferred report UI port (default: `4321`; env `GORAY_PORT`; `0` lets the OS pick). On `EADDRINUSE`, the next free port is tried automatically.
* `--port-range <n>` — candidate window size after the preferred port (default: `100`; env `GORAY_PORT_RANGE`).
* `--strict-port` — fail instead of moving when the preferred port is busy (env `GORAY_STRICT_PORT=true`).
* `--host <addr>` — bind address (default: `127.0.0.1`; env `GORAY_HOST`). Non-loopback binds print a warning.
* `--port-file <path>` — write the chosen URL and PID to a file and remove it on clean shutdown (env `GORAY_PORT_FILE`). Useful for following a moved port from the Vite dev proxy.
* `--tags <list>` — Go build tags, comma- or space-separated (env `GORAY_BUILD_TAGS`).
* `--workspace` — index every module in the active `go.work` as one semantic graph.
* `--open` — open the local report URL in the default browser.
* `--no-watch` — disable `fsnotify` watching and background reindexing.

`serve` also runs a local read-only report UI and prints a Vite-style banner to **stderr**, keeping stdout reserved for MCP JSON-RPC:

```text
  goray dev

  ➜  Local:   http://127.0.0.1:4321/
  ➜  MCP:     stdio
  ➜  Press Ctrl+C to stop
  GORAY_REPORT_URL=http://127.0.0.1:4321/
```

When the port moves, stderr also prints `Port 4321 is in use, trying another one...`. The
machine-readable `GORAY_REPORT_URL=` line stays on stderr so stdout remains a clean MCP
JSON-RPC channel. All candidate ports busy in non-strict mode is an error listing the tried
range; non-`EADDRINUSE` bind failures propagate unchanged.

The report reflects the current index snapshot and summary cache. The live page reads `GET /api/report` every five seconds and updates in place, preserving the active view, filters, selection, and graph zoom. The initial index builds in the background. File edits trigger debounced rebuilds; incoming queries wait briefly for an in-flight rebuild or serve the last healthy snapshot while reporting `index_state`. Parse or type errors in production code never overwrite the last good snapshot; test-only compile errors are surfaced as warnings.

### `export`

```sh
goray export <html|md|dot> [path] [-o <file>] [--db <path>] [--tags <list>] [--workspace] [--open]

```

* `-o, --out <file>` — write to a file instead of `stdout`.
* `--db <path>` — SQLite cache path used to enrich `html`/`md` exports with cached summaries.
* `--tags <list>` / `--workspace` — same build-tag and `go.work` semantics as `serve`/`index`.
* `--open` — open the generated report in the default browser (`html` only). Without `-o`, writes to `$TMPDIR/goray-<repo-slug>-report.html` and prints the path to `stderr`.

```sh
goray export html --open
goray export html -o report.html /path/to/repo
goray export md -o ARCHITECTURE.md
goray export dot | dot -T svg > deps.svg

```

The HTML export is a single offline file with all CSS, JS, and indexed metadata embedded (no external assets, no source dumps). The UI is a React + TypeScript + Tailwind build compiled by Vite into `internal/render/dist/report.html` and embedded with `go:embed`. It includes a viewport-locked workbench with graph, hotspots, interfaces, and explorer views, a resizable inspector, and a `Cmd+K` command palette. The committed artifact means `go install`, `go build`, and `go test` never require Node.js.

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
goray index [path] [--db <path>] [--tags <list>] [--workspace] [--json]
```

Builds the semantic index exactly once and syncs `working_symbols` and
`working_edges` to SQLite. It does not start fsnotify, the MCP stdio server, or
the local report UI.

```sh
goray index
goray index /path/to/repo --json
goray index --db :memory: .
```

`--json` emits a single object with `root`, `module_path`, `module_paths`,
`build_tags`, `workspace`, `build_mode`, `db_path`, `summary_db_path`,
`packages`, `files`, `symbols`, `edges`, `eligible`, `synced`,
`coverage_percent`, `warnings`, and `duration_ms`.

Build tags accept comma- or space-separated values and `GORAY_BUILD_TAGS`.
Normalized tag sets get a distinct working DB (order-independent), so
`--tags integration` never overwrites the default snapshot; the shared summary
DB stays content-addressed. Files gated behind `//go:build` tags participate in
test reachability and dead-code analysis only when their tags are active.

`--workspace` (or a workspace-root path containing `go.work` without `go.mod`)
loads every `use` module into one semantic graph. `GOWORK=off` disables
workspace detection; a path inside one module still resolves to that module
unless `--workspace` is explicit.

Production parse/type errors exit `1` without overwriting an existing healthy
working snapshot. Errors confined to `*_test.go` files sync production state,
print warnings to stderr, and exit `0`.

### `diff`

```sh
goray diff [base-ref] [path] [--format md|json] [--fail-on <gates>] [--tags <list>] [--workspace]
```

Indexes the working tree against a temporary detached git worktree of
`base-ref` (default `HEAD`) without touching the user's checkout, then reports:

* new and resolved cross-module cycles;
* removed/modified exported symbols (breaking) and added exported symbols;
* new untested production functions with cyclomatic complexity >= 10, plus the
  largest complexity/LOC deltas;
* newly introduced and newly removed dead symbols;
* package, file, LOC, symbol, cross-module edge, and untested-hotspot counters.

`--fail-on` accepts `cycles`, `dead_code`, `breaking_api`, and
`untested_hotspots` (comma-separated). Triggered gates exit `1`; success exits
`0`. A missing git repository or unresolvable base ref exits `2`. Temporary
worktrees are always removed, including on error.

```sh
goray diff main --fail-on cycles,breaking_api
goray diff origin/main . --format json > diff.json
```

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
| `get_codebase_map` | Compact module scorecard with generated symbol/LOC rollups, cross-module edge counts, cycles, large files, top hotspots, pagination metadata; `detail`, `summary_only`, `top_modules`, `sort_by`, `tracked_only` (`md`/`json`/`dot`/`mermaid`) |
| `get_module_overview` | Compact package rollups, dependency edges, large files, and top hotspots with `include_generated`; `detail=full` for the legacy projection (`md`/`json`) |
| `get_dependency_graph` | Package BFS dependency graph and fan-in/fan-out ranking (`json`/`mermaid`/`dot`) |
| `explain_dependency` | Exact imports, `file:line` symbol usages, blank/dot imports, enclosing callers, and counters behind a module/package edge |

### Symbols, calls, and impact

| Tool | Purpose |
| --- | --- |
| `lookup_symbol` | Canonical ID/full Go path/repo-relative/fuzzy symbol lookup with cached or enclosing summary metadata |
| `get_api_surface` | Paginated exported symbols with generated-symbol collapsing, kind filter, and cached summary metadata |
| `get_references` | Type-checked references with enclosing caller and `file:line` |
| `get_call_graph` | BFS callers/callees with typed `direct` and in-module `interface_dispatch` edges |
| `find_implementations` | `go/types.Implements` interface-to-type matrix, including test mocks |
| `get_impact_analysis` | Kind-aware blast radius: implementations, test mocks, direct refs, composite literals, transitive callers, and test packages |
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
internal/render/dist     Committed single-file Vite build (report.html)
web                      Quarantined React/TypeScript/Tailwind report source
.opencode/skills/goray   OpenCode skill definition
scripts/mcp-config.py    MCP client config installer for make install-mcp

```

## Development

```sh
make verify       # gofmt check, go vet, go test -race -count=1, golangci-lint
make web-install  # npm ci inside web/
make web-dev      # Vite dev server; proxies /api to http://127.0.0.1:4321
make web-build    # typecheck + build internal/render/dist/report.html
```

`make build` and `make verify` stay Node-free. Only `make web-build` requires Node.js, and its output is committed. For frontend work, run `goray serve` in one terminal and `make web-dev` in another; edit `web/src` without rebuilding the Go binary. If `serve` picks a port other than `4321`, point the proxy at it with `GORAY_DEV_API=http://127.0.0.1:<port> make web-dev`.

Release artifacts are built with [GoReleaser](https://goreleaser.com/) when a
`v*` tag is pushed. Validate the release config with `make release-check`, or
build local snapshot artifacts with `make release-snapshot`. See
[CHANGELOG.md](CHANGELOG.md) for release history.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, tests, coding
rules, and the release process. Participation is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Security

Report vulnerabilities privately through
[GitHub Security Advisories](https://github.com/yanulabs/goray/security/advisories/new).
See [SECURITY.md](SECURITY.md) for scope and supported versions.

## License

MIT. See [LICENSE](LICENSE).

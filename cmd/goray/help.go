package main

import (
	"fmt"
	"io"
)

func printRootHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `goray — semantic index and MCP server for Go codebases

Usage:
  goray <command> [arguments]

Commands:
  serve [path]                 Run the MCP stdio server + local report UI (default: .)
  export <html|md|dot> [path]  Export an architecture report or dependency graph
  db <info|path|rm|prune>      Inspect, locate, remove, or prune SQLite databases
  index [path]                 Build and sync the semantic index once
  diff [base-ref] [path]       Compare architecture against a git ref (CI gate)
  version                      Print version and exit
  help [command]               Show help for goray or a command

Flags:
  -h, --help                   Show this help
  -V, --version                Print version and build information

Environment:
  GORAY_DB            SQLite cache path (supports :memory:)
  GORAY_LOG_LEVEL     serve log level: off, error, or info (default: info)
  GORAY_PORT          serve preferred port (default: 4321)
  GORAY_PORT_RANGE    serve fallback window size (default: 100)
  GORAY_HOST          serve bind host (default: 127.0.0.1)
  GORAY_STRICT_PORT   fail instead of moving to another port
  GORAY_PORT_FILE     write the chosen report URL and PID to a file
  GORAY_BUILD_TAGS    Go build tags (comma- or space-separated)

Examples:
  goray serve
  goray serve /path/to/repo --open
  goray export html --open
  goray export html -o report.html
  goray export dot | dot -Tsvg > deps.svg
  goray index --json .
  goray diff main --fail-on cycles,breaking_api
  sqlite3 "$(goray db path)"
  goray db info
  goray db rm --dry-run
  goray db prune
`)
}

func printServeHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  goray serve [flags] [path]

Run the MCP stdio server and a local architecture report UI for the nearest Go
module. The path defaults to ".". The report URL is printed to stderr so stdout
stays reserved for MCP JSON-RPC.

Flags:
  --db <path>          SQLite cache path (default: user cache directory; env GORAY_DB)
  --log-level <level>  Log level: off, error, or info (default: info; env GORAY_LOG_LEVEL)
  --port <n>           Preferred HTTP port (default: 4321; env GORAY_PORT; 0 = OS pick)
  --port-range <n>     Fallback window size (default: 100; env GORAY_PORT_RANGE)
  --strict-port        Fail when the preferred port is busy (env GORAY_STRICT_PORT)
  --host <addr>        Bind address (default: 127.0.0.1; env GORAY_HOST)
  --port-file <path>   Write chosen URL and PID; removed on shutdown (env GORAY_PORT_FILE)
  --tags <list>        Go build tags (env GORAY_BUILD_TAGS)
  --workspace          Index all modules in the active go.work
  --retain-packages    Keep AST/type data for faster incremental rebuilds (more memory)
  --open               Open the local report in the default browser
  --no-watch           Disable fsnotify watching and background reindexing
  -h, --help           Show this help

Examples:
  goray serve
  goray serve /path/to/repo --open
  goray serve /path/to/repo --no-watch
  goray serve --port 0
  goray serve --port 4321 --strict-port
  goray serve --host 0.0.0.0 --port-file /tmp/goray-port
  goray serve --tags integration --workspace
  goray serve --db :memory: --log-level error
`)
}

func printExportHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  goray export <html|md|dot> [flags] [path]

Export a self-contained HTML report, a Markdown architecture map, or a Graphviz
DOT graph. The path defaults to ".". Without -o, output is written to stdout.

Flags:
  -o, --out <file>  Write output to a file instead of stdout
  --db <path>       SQLite cache path for cached HTML/Markdown summaries
                    (default: user cache directory; env GORAY_DB)
  --tags <list>     Go build tags (comma- or space-separated; env GORAY_BUILD_TAGS)
  --workspace       Index all modules in the active go.work
  --open            Open the HTML report in the default browser (HTML only)
  -h, --help        Show this help

Examples:
  goray export html --open
  goray export html -o report.html
  goray export md . -o ARCHITECTURE.md
  goray export dot | dot -Tsvg > deps.svg
  goray export dot /path/to/repo --out deps.dot
`)
}

func printDBHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  goray db <info|path|rm|prune> [flags] [path]

Inspect, locate, remove, or prune the SQLite databases. The path defaults to
".". By default two databases are used: a per-checkout working database under
<user-cache>/goray/worktrees/ and a shared summary database under
<user-cache>/goray/summaries/. Worktrees and clones of the same module share
summaries while keeping working state isolated.

Subcommands:
  info    Print cache statistics and recent summaries as JSON
  path    Print only the resolved working database path
  rm      Remove the working DB (default), summary DB, or both
  prune   Remove worktree DBs whose checkout directory no longer exists

Flags:
  --db <path>   Single-database override for info/path/rm (env GORAY_DB)
  --tags <list> Build tags for working DB selection (env GORAY_BUILD_TAGS)
  --workspace   Resolve paths in the active go.work
  --summaries   With "path" or "rm", target only the shared summary database
  --all         With "rm", remove both working and summary databases
  --dry-run     Print exact files and sizes without deleting (rm/prune)
  --force       Remove even if another process holds the DB lock (rm/prune)
  -h, --help    Show this help

Examples:
  goray db path
  goray db path --summaries
  goray db info /path/to/repo
  goray db rm                       # working DB only; summaries untouched
  goray db rm --summaries           # shared summary DB only
  goray db rm --all --dry-run
  goray db rm --db /tmp/goray.db
  goray db rm --force .
  goray db prune --dry-run
  sqlite3 "$(goray db path)"
`)
}

func printIndexHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  goray index [flags] [path]

Build the semantic index once, sync working symbols and edges to SQLite, print
a summary, and exit. This does not start file watching, MCP stdio, or the local
report UI.

By default the working state is written to a per-checkout database under
<user-cache>/goray/worktrees/ and summaries are read/written in a shared
database under <user-cache>/goray/summaries/. --db or GORAY_DB switches to a
single database containing all tables.

Flags:
  --db <path>    Single SQLite database override (default: user cache directory;
                 env GORAY_DB; supports :memory:)
  --tags <list>  Go build tags (comma- or space-separated; env GORAY_BUILD_TAGS)
  --workspace    Index all modules in the active go.work
  --json         Emit one machine-readable JSON object to stdout
  -h, --help     Show this help

Examples:
  goray index
  goray index /path/to/repo
  goray index --json .
  goray index --tags integration .
  goray index --workspace /path/to/workspace
  goray index . --db :memory:
  goray index --db /tmp/goray.db /path/to/repo
`)
}

func printDiffHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  goray diff [base-ref] [path] [flags]

Index the working tree and a detached temporary worktree of base-ref, then
report deterministic architectural deltas: new/resolved cross-module cycles,
breaking exported API changes, new untested complexity hotspots, new dead code,
and package/file/LOC/symbol/edge counters. base-ref defaults to HEAD.

The user's working tree is never modified. The temporary worktree is removed
even when indexing fails.

Flags:
  --format <md|json>  Output format (default: md)
  --fail-on <gates>   Comma-separated gates that exit 1 when triggered:
                      cycles, dead_code, breaking_api, untested_hotspots
  --tags <list>       Go build tags (comma- or space-separated)
  --workspace         Index all modules in the active go.work
  -h, --help          Show this help

Examples:
  goray diff
  goray diff main
  goray diff origin/main . --format json
  goray diff main --fail-on cycles,dead_code,breaking_api,untested_hotspots
`)
}

func printVersionHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  goray version [--short|--json]

Print the version, commit, build time, Go version, and platform. Use --short for
the version alone or --json for machine-readable metadata. Top-level -v, -V,
and --version are also accepted.

Flags:
  --short   Print only the version
  --json    Print version metadata as one JSON object
  -h, --help Show this help

Examples:
  goray version
  goray version --short
  goray version --json
  goray -V
`)
}

func runHelp(args []string, stdout io.Writer) error {
	if len(args) == 0 || wantsHelp(args, nil) {
		printRootHelp(stdout)
		return nil
	}
	if len(args) > 1 {
		return usagef("help accepts at most one command")
	}
	switch args[0] {
	case "serve":
		printServeHelp(stdout)
	case "export":
		printExportHelp(stdout)
	case "db":
		printDBHelp(stdout)
	case "index":
		printIndexHelp(stdout)
	case "diff":
		printDiffHelp(stdout)
	case "version":
		printVersionHelp(stdout)
	case "help":
		printRootHelp(stdout)
	default:
		return usagef("unknown command %q", args[0])
	}
	return nil
}

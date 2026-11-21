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
  db <info|path> [path]        Inspect or locate the SQLite summary cache
  version                      Print version and exit
  help [command]               Show help for goray or a command

Environment:
  GORAY_DB          SQLite cache path (supports :memory:)
  GORAY_LOG_LEVEL   serve log level: off, error, or info (default: info)

Examples:
  goray serve
  goray serve /path/to/repo --open
  goray export html --open
  goray export html -o report.html
  goray export dot | dot -Tsvg > deps.svg
  sqlite3 "$(goray db path)"
  goray db info
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
  --port <n>           Local HTTP port (default: 4321; 0 picks a free port)
  --open               Open the local report in the default browser
  --no-watch           Disable fsnotify watching and background reindexing
  -h, --help           Show this help

Examples:
  goray serve
  goray serve /path/to/repo --open
  goray serve /path/to/repo --no-watch
  goray serve --port 0
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
  goray db <info|path> [flags] [path]

Inspect or locate the SQLite summary cache. The path defaults to ".".

Subcommands:
  info   Print cache statistics and recent summaries as JSON
  path   Print only the resolved cache path

Flags:
  --db <path>  SQLite cache path override (default: user cache directory; env GORAY_DB)
  -h, --help   Show this help

Examples:
  goray db path
  goray db path /path/to/repo
  goray db path --db :memory:
  goray db info /path/to/repo
  sqlite3 "$(goray db path)"
`)
}

func printVersionHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  goray version

Print the build version and exit. Top-level -v and --version are also accepted.

Examples:
  goray version
  goray -v
  goray --version
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
	case "version":
		printVersionHelp(stdout)
	case "help":
		printRootHelp(stdout)
	default:
		return usagef("unknown command %q", args[0])
	}
	return nil
}

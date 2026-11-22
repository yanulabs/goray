package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

var version = "dev"

func buildVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr, openBrowser))
}

func runCLI(args []string, stdout, stderr io.Writer, opener func(string) error) int {
	err := execute(args, stdout, stderr, opener)
	if err == nil {
		return 0
	}
	var usage *usageError
	if errors.As(err, &usage) {
		_, _ = fmt.Fprintf(stderr, "goray: %s\n", usage.Error())
		_, _ = fmt.Fprintln(stderr, "Run 'goray help <command>' for usage.")
		return 2
	}
	_, _ = fmt.Fprintf(stderr, "goray: %s\n", err)
	return 1
}

func execute(args []string, stdout, stderr io.Writer, opener func(string) error) error {
	if len(args) == 0 {
		printRootHelp(stdout)
		return nil
	}
	switch args[0] {
	case "-h", "--help", "-help":
		printRootHelp(stdout)
		return nil
	case "-v", "--version":
		printVersion(stdout)
		return nil
	case "version":
		if wantsHelp(args[1:], nil) {
			printVersionHelp(stdout)
			return nil
		}
		if len(args) > 1 {
			return usagef("version takes no arguments")
		}
		printVersion(stdout)
		return nil
	case "help":
		return runHelp(args[1:], stdout)
	case "serve":
		if wantsHelp(args[1:], serveValueFlags) {
			printServeHelp(stdout)
			return nil
		}
		return runServe(args[1:], stderr, opener)
	case "export":
		if len(args) > 1 && wantsHelp(args[1:], exportValueFlags) {
			printExportHelp(stdout)
			return nil
		}
		return runExport(args[1:], stdout, stderr, opener)
	case "db":
		if len(args) > 1 && wantsHelp(args[1:], dbValueFlags) {
			printDBHelp(stdout)
			return nil
		}
		return runDB(args[1:], stdout)
	case "index":
		if len(args) > 1 && wantsHelp(args[1:], indexValueFlags) {
			printIndexHelp(stdout)
			return nil
		}
		return runIndex(args[1:], stdout, stderr)
	case "diff":
		if len(args) > 1 && wantsHelp(args[1:], diffValueFlags) {
			printDiffHelp(stdout)
			return nil
		}
		return runDiff(args[1:], stdout, stderr)
	default:
		if strings.HasPrefix(args[0], "-") {
			return usagef("unknown flag %q", args[0])
		}
		return usagef("unknown command %q", args[0])
	}
}

func printVersion(w io.Writer) {
	_, _ = fmt.Fprintf(w, "goray %s\n", buildVersion())
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

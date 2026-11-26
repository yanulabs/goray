package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

var dbValueFlags = map[string]bool{"db": true, "tags": true}

type dbOptions struct {
	command   string
	path      string
	dbPath    string
	tags      []string
	workspace bool
	summaries bool
	all       bool
	dryRun    bool
	force     bool
}

func parseDBArgs(args []string) (dbOptions, error) {
	if len(args) == 0 {
		return dbOptions{}, usagef("db requires a subcommand: info, path, rm, or prune")
	}
	opts := dbOptions{command: args[0]}
	switch opts.command {
	case "info", "path", "rm", "prune":
	default:
		return dbOptions{}, usagef("unknown db subcommand %q (use info, path, rm, or prune)", opts.command)
	}

	fs := flag.NewFlagSet("db "+opts.command, flag.ContinueOnError)
	quietFlagSet(fs)
	fs.StringVar(&opts.dbPath, "db", "", "SQLite cache path")
	var tags string
	fs.StringVar(&tags, "tags", "", "Go build tags (comma- or space-separated)")
	fs.BoolVar(&opts.workspace, "workspace", false, "resolve paths in the active go.work")
	fs.BoolVar(&opts.summaries, "summaries", false, "target the shared summary database")
	fs.BoolVar(&opts.all, "all", false, "target both the working and summary databases")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print targets without deleting them")
	fs.BoolVar(&opts.force, "force", false, "remove even if another process holds a lock")
	positionals, err := parseFlagSet(fs, args[1:])
	if err != nil {
		return dbOptions{}, err
	}
	opts.tags = buildTags(tags)
	if opts.command == "prune" {
		if len(positionals) != 0 {
			return dbOptions{}, usagef("db prune accepts no path")
		}
		opts.path = "."
		return opts, nil
	}
	opts.path, err = singlePath(positionals, "db "+opts.command)
	return opts, err
}

func runDB(args []string, stdout io.Writer) error {
	opts, err := parseDBArgs(args)
	if err != nil {
		return err
	}
	if opts.command == "prune" {
		return runDBPrune(opts, stdout)
	}
	if opts.command == "rm" {
		if dbPath, ok := explicitDBPath(opts.dbPath); ok {
			if dbPath == ":memory:" {
				return usagef("refusing to remove :memory: database")
			}
			result, removeErr := cache.RemoveDB(dbPath, removeOptions(opts))
			if removeErr != nil {
				return removeErr
			}
			printRemoved(stdout, result, opts.dryRun)
			return nil
		}
	}
	root, err := index.ResolveProjectRoot(opts.path, opts.workspace)
	if err != nil {
		return err
	}
	if opts.command == "path" {
		var dbPath string
		pathOpts := cache.PathOptions{Tags: opts.tags, Workspace: opts.workspace}
		if opts.summaries {
			dbPath, err = cache.ResolveSummaryPathWithOptions(root, opts.dbPath, pathOpts)
		} else {
			dbPath, err = cache.ResolvePathWithOptions(root, opts.dbPath, pathOpts)
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, dbPath)
		return nil
	}
	if opts.command == "rm" {
		return runDBRemove(opts, root, stdout)
	}

	workingPath, summaryPath, err := cache.ResolvePathsWithOptions(root, opts.dbPath, cache.PathOptions{
		Tags: opts.tags, Workspace: opts.workspace,
	})
	if err != nil {
		return err
	}
	summaries, err := cache.OpenSplit(workingPath, summaryPath)
	if err != nil {
		return fmt.Errorf("open summary cache: %w", err)
	}
	stats, statsErr := summaries.Stats(context.Background())
	closeErr := summaries.Close()
	if statsErr != nil {
		return statsErr
	}
	if closeErr != nil {
		return closeErr
	}
	_, _ = fmt.Fprintln(stdout, render.JSON(stats))
	return nil
}

func explicitDBPath(flagPath string) (string, bool) {
	if flagPath != "" {
		return flagPath, true
	}
	if env := os.Getenv("GORAY_DB"); env != "" {
		return env, true
	}
	return "", false
}

func removeOptions(opts dbOptions) cache.RemoveOptions {
	return cache.RemoveOptions{DryRun: opts.dryRun, Force: opts.force}
}

func printRemoved(stdout io.Writer, result cache.RemoveResult, dryRun bool) {
	if len(result.Files) == 0 {
		_, _ = fmt.Fprintln(stdout, "nothing to remove")
		return
	}
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	for _, file := range result.Files {
		_, _ = fmt.Fprintf(stdout, "%s %s (%d bytes)\n", verb, file.Path, file.Size)
	}
}

func runDBRemove(opts dbOptions, root string, stdout io.Writer) error {
	removeFlags := removeOptions(opts)
	working, summary, err := cache.ResolvePathsWithOptions(root, "", cache.PathOptions{
		Tags: opts.tags, Workspace: opts.workspace,
	})
	if err != nil {
		return err
	}
	targets := []string{working}
	switch {
	case opts.summaries:
		targets = []string{summary}
	case opts.all:
		targets = []string{working, summary}
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if target == "" || seen[target] || target == ":memory:" {
			continue
		}
		seen[target] = true
		result, err := cache.RemoveDB(target, removeFlags)
		if err != nil {
			return err
		}
		printRemoved(stdout, result, opts.dryRun)
	}
	return nil
}

func runDBPrune(opts dbOptions, stdout io.Writer) error {
	result, err := cache.Prune(removeOptions(opts))
	if err != nil {
		return err
	}
	verb := "removed"
	if opts.dryRun {
		verb = "would remove"
	}
	for _, file := range result.Removed {
		_, _ = fmt.Fprintf(stdout, "%s %s (%d bytes)\n", verb, file.Path, file.Size)
	}
	for _, skipped := range result.Skipped {
		_, _ = fmt.Fprintf(stdout, "skipped %s: %s\n", skipped.Path, skipped.Reason)
	}
	if len(result.Removed) == 0 && len(result.Skipped) == 0 {
		_, _ = fmt.Fprintln(stdout, "nothing to prune")
	}
	_, _ = fmt.Fprintf(stdout, "reclaimed bytes: %d\n", result.ReclaimedBytes)
	return nil
}

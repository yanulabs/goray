package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

var dbValueFlags = map[string]bool{"db": true}

type dbOptions struct {
	command   string
	path      string
	dbPath    string
	summaries bool
}

func parseDBArgs(args []string) (dbOptions, error) {
	if len(args) == 0 {
		return dbOptions{}, usagef("db requires a subcommand: info or path")
	}
	opts := dbOptions{command: args[0]}
	switch opts.command {
	case "info", "path":
	default:
		return dbOptions{}, usagef("unknown db subcommand %q (use info or path)", opts.command)
	}

	fs := flag.NewFlagSet("db "+opts.command, flag.ContinueOnError)
	quietFlagSet(fs)
	fs.StringVar(&opts.dbPath, "db", "", "SQLite cache path")
	fs.BoolVar(&opts.summaries, "summaries", false, "print the shared summary database path")
	positionals, err := parseFlagSet(fs, args[1:])
	if err != nil {
		return dbOptions{}, err
	}
	opts.path, err = singlePath(positionals, "db "+opts.command)
	if err != nil {
		return dbOptions{}, err
	}
	return opts, nil
}

func runDB(args []string, stdout io.Writer) error {
	opts, err := parseDBArgs(args)
	if err != nil {
		return err
	}
	root, err := index.ResolveModuleRoot(opts.path)
	if err != nil {
		return err
	}
	if opts.command == "path" {
		var dbPath string
		if opts.summaries {
			dbPath, err = cache.ResolveSummaryPath(root, opts.dbPath)
		} else {
			dbPath, err = cache.ResolvePath(root, opts.dbPath)
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, dbPath)
		return nil
	}
	workingPath, summaryPath, err := cache.ResolvePaths(root, opts.dbPath)
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

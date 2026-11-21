package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
	"github.com/yanulabs/goray/internal/tools"
	"github.com/yanulabs/goray/internal/watch"
)

var serveValueFlags = map[string]bool{"db": true, "log-level": true, "port": true}

type serveOptions struct {
	path     string
	dbPath   string
	logLevel string
	port     int
	noWatch  bool
	open     bool
}

func parseServeArgs(args []string) (serveOptions, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	quietFlagSet(fs)
	var opts serveOptions
	fs.StringVar(&opts.dbPath, "db", "", "SQLite cache path")
	fs.StringVar(&opts.logLevel, "log-level", envOr("GORAY_LOG_LEVEL", "info"), "log level")
	fs.IntVar(&opts.port, "port", defaultServePort, "local HTTP port")
	fs.BoolVar(&opts.noWatch, "no-watch", false, "disable watching")
	fs.BoolVar(&opts.open, "open", false, "open local report")
	positionals, err := parseFlagSet(fs, args)
	if err != nil {
		return serveOptions{}, err
	}
	if opts.port < 0 || opts.port > 65535 {
		return serveOptions{}, usagef("--port must be between 0 and 65535")
	}
	opts.path, err = singlePath(positionals, "serve")
	if err != nil {
		return serveOptions{}, err
	}
	return opts, nil
}

func runServe(args []string, stderr io.Writer, opener func(string) error) error {
	opts, err := parseServeArgs(args)
	if err != nil {
		return err
	}
	return serve(opts, stderr, opener)
}

func serve(opts serveOptions, stderr io.Writer, opener func(string) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveContext(ctx, opts, os.Stdin, os.Stdout, stderr, opener)
}

func serveContext(ctx context.Context, opts serveOptions, stdin io.Reader, stdout, stderr io.Writer, opener func(string) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	root, err := index.ResolveModuleRoot(opts.path)
	if err != nil {
		return err
	}
	level, err := parseLevel(opts.logLevel)
	if err != nil {
		return usagef("%s", err)
	}
	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: level}))
	formats := render.NewRegistry()

	dbPath, summaryDBPath, err := cache.ResolvePaths(root, opts.dbPath)
	if err != nil {
		return err
	}
	summaries, err := cache.OpenSplit(dbPath, summaryDBPath)
	if err != nil {
		return fmt.Errorf("open summary cache: %w", err)
	}
	defer func() {
		if err := summaries.Close(); err != nil {
			logger.Error("close summary cache", "err", err)
		}
	}()

	listener, err := listenLocal(opts.port)
	if err != nil {
		return err
	}
	url := localURL(listener)
	printServeBanner(stderr, url)
	if opts.open && opener != nil {
		if err := opener(url); err != nil {
			logger.Warn("open local report", "err", err)
		}
	}

	logger.Info("summary cache ready", "worktree_db", summaries.Path(), "summary_db", summaries.SummaryPath())

	store := index.NewStore(root, logger)
	store.SetBuildHook(func(idx *index.Index) error {
		return summaries.Sync(context.Background(), idx)
	})
	store.Start()
	defer store.WaitForBuild()

	reportServer := startReportServer(ctx, listener, store, summaries, logger)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = reportServer.Shutdown(shutdownCtx)
	}()

	if !opts.noWatch {
		watcher, err := watch.New(root, logger, store.MarkDirty)
		if err != nil {
			logger.Warn("file watcher unavailable", "err", err)
		} else {
			defer func() { _ = watcher.Close() }()
			store.StartAutoRebuild(ctx)
			defer store.WaitForAuto()
		}
	}

	server := mcp.NewServer(stdin, stdout, tools.New(store, formats, summaries))
	server.SetVersion(buildVersion())
	return server.Serve(ctx)
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "", "info":
		return slog.LevelInfo, nil
	case "error":
		return slog.LevelError, nil
	case "off":
		return slog.Level(127), nil
	default:
		return 0, fmt.Errorf("invalid log level %q (use off, error, or info)", value)
	}
}

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
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

var serveValueFlags = map[string]bool{
	"db": true, "log-level": true, "port": true, "tags": true,
	"host": true, "port-range": true, "port-file": true,
}

type serveOptions struct {
	path       string
	dbPath     string
	logLevel   string
	tags       string
	host       string
	portFile   string
	workspace  bool
	port       int
	portRange  int
	strictPort bool
	noWatch    bool
	open       bool
}

func parseServeArgs(args []string) (serveOptions, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	quietFlagSet(fs)
	var opts serveOptions
	fs.StringVar(&opts.dbPath, "db", "", "SQLite cache path")
	fs.StringVar(&opts.logLevel, "log-level", envOr("GORAY_LOG_LEVEL", "info"), "log level")
	fs.IntVar(&opts.port, "port", envInt("GORAY_PORT", defaultServePort), "local HTTP port")
	fs.IntVar(&opts.portRange, "port-range", envInt("GORAY_PORT_RANGE", defaultPortRange), "candidate port window")
	fs.StringVar(&opts.host, "host", envOr("GORAY_HOST", defaultServeHost), "bind host")
	fs.StringVar(&opts.portFile, "port-file", os.Getenv("GORAY_PORT_FILE"), "write chosen URL and PID")
	fs.BoolVar(&opts.strictPort, "strict-port", envBool("GORAY_STRICT_PORT", false), "fail instead of moving to another port")
	fs.StringVar(&opts.tags, "tags", "", "Go build tags (comma- or space-separated)")
	fs.BoolVar(&opts.workspace, "workspace", false, "index all modules in the active go.work")
	fs.BoolVar(&opts.noWatch, "no-watch", false, "disable watching")
	fs.BoolVar(&opts.open, "open", false, "open local report")
	positionals, err := parseFlagSet(fs, args)
	if err != nil {
		return serveOptions{}, err
	}
	if opts.port < 0 || opts.port > 65535 {
		return serveOptions{}, usagef("--port must be between 0 and 65535")
	}
	if opts.portRange <= 0 || opts.portRange > 65535 {
		return serveOptions{}, usagef("--port-range must be between 1 and 65535")
	}
	if opts.host == "" {
		return serveOptions{}, usagef("--host must not be empty")
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

	root, err := index.ResolveProjectRoot(opts.path, opts.workspace)
	if err != nil {
		return err
	}
	tags := buildTags(opts.tags)
	level, err := parseLevel(opts.logLevel)
	if err != nil {
		return usagef("%s", err)
	}
	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: level}))
	formats := render.NewRegistry()

	dbPath, summaryDBPath, err := cache.ResolvePathsWithTags(root, opts.dbPath, tags)
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

	listenCfg := listenConfig{
		port: opts.port, portRange: opts.portRange, host: opts.host, strict: opts.strictPort,
	}
	reserved, err := listenConfigured(listenCfg)
	if err != nil {
		return err
	}
	listener := reserved.listener
	url := localURL(listener)
	if reserved.fallback {
		_, _ = fmt.Fprintf(stderr, "  Port %d is in use, trying another one...\n", reserved.requested)
	}
	if !isLoopbackHost(opts.host) {
		_, _ = fmt.Fprintf(stderr, "  warning: binding non-loopback host %s; the report UI is network-accessible\n", opts.host)
	}
	printServeBanner(stderr, url)
	_, _ = fmt.Fprintf(stderr, "  GORAY_REPORT_URL=%s\n", url)
	if opts.portFile != "" {
		if err := writePortFile(opts.portFile, url); err != nil {
			_ = listener.Close()
			return err
		}
		defer func() { _ = os.Remove(opts.portFile) }()
	}
	if opts.open && opener != nil {
		if err := opener(url); err != nil {
			logger.Warn("open local report", "err", err)
		}
	}

	logger.Info("summary cache ready", "worktree_db", summaries.Path(), "summary_db", summaries.SummaryPath())

	store := index.NewStoreWithOptions(root, logger, index.BuildOptions{Tags: tags, Workspace: opts.workspace})
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
		watcher, err := watch.New(root, logger, func(paths []string) { store.MarkDirty(paths...) })
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

// writePortFile publishes the chosen URL and PID so the Vite dev proxy and
// scripts can follow a moved port without scraping stderr.
func writePortFile(path, url string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("GORAY_REPORT_URL=%s\nPID=%d\n", url, os.Getpid())
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

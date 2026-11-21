package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/yanulabs/go-mcp-server/internal/cache"
	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/mcp"
	"github.com/yanulabs/go-mcp-server/internal/render"
	"github.com/yanulabs/go-mcp-server/internal/tools"
	"github.com/yanulabs/go-mcp-server/internal/watch"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	exportDOT := flag.String("export-dot", "", "export the module graph as Graphviz DOT and exit")
	exportMD := flag.String("export-md", "", "export the architecture map as Markdown and exit")
	dbFlag := flag.String("db", "", "SQLite cache path (default: user cache dir; env GO_MCP_DB)")
	noWatch := flag.Bool("no-watch", false, "disable automatic file watching and reindexing")
	logLevelFlag := flag.String("log-level", envOr("GO_MCP_LOG_LEVEL", "info"), "log level: off|error|info")
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	level, err := parseLevel(*logLevelFlag)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	formats := render.NewRegistry()

	if *exportDOT != "" || *exportMD != "" {
		return export(root, *exportDOT, *exportMD, logger, formats)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbPath, err := cache.ResolvePath(root, *dbFlag)
	if err != nil {
		return err
	}
	summaries, err := cache.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open summary cache: %w", err)
	}
	defer func() {
		if err := summaries.Close(); err != nil {
			logger.Error("close summary cache", "err", err)
		}
	}()
	logger.Info("summary cache ready", "path", summaries.Path())

	store := index.NewStore(root, logger)
	store.SetBuildHook(func(idx *index.Index) error {
		return summaries.Sync(context.Background(), idx)
	})
	store.Start()
	defer store.WaitForBuild()

	if !*noWatch {
		watcher, err := watch.New(root, logger, store.MarkDirty)
		if err != nil {
			logger.Warn("file watcher unavailable", "err", err)
		} else {
			defer func() { _ = watcher.Close() }()
			store.StartAutoRebuild(ctx)
			defer store.WaitForAuto()
		}
	}

	server := mcp.NewServer(os.Stdin, os.Stdout, tools.New(store, formats, summaries))
	err = server.Serve(ctx)
	stop()
	return err
}

func export(root, dotPath, mdPath string, logger *slog.Logger, formats *render.Registry) error {
	idx, err := index.Build(root)
	if err != nil {
		return err
	}
	if dotPath != "" {
		content, ok := formats.RenderCodebaseMap("dot", idx)
		if !ok {
			return fmt.Errorf("dot renderer is not registered")
		}
		if err := os.WriteFile(dotPath, []byte(content), 0o644); err != nil {
			return err
		}
		logger.Info("wrote DOT", "path", dotPath, "modules", len(idx.Modules))
	}
	if mdPath != "" {
		content, ok := formats.RenderCodebaseMap("markdown", idx)
		if !ok {
			return fmt.Errorf("markdown renderer is not registered")
		}
		if err := os.WriteFile(mdPath, []byte(content), 0o644); err != nil {
			return err
		}
		logger.Info("wrote Markdown", "path", mdPath, "modules", len(idx.Modules))
	}
	return nil
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

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

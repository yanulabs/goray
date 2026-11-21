package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/yanulabs/go-mcp-server/internal/cache"
	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/mcp"
	"github.com/yanulabs/go-mcp-server/internal/render"
	"github.com/yanulabs/go-mcp-server/internal/report"
	"github.com/yanulabs/go-mcp-server/internal/tools"
	"github.com/yanulabs/go-mcp-server/internal/watch"
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
	if err := run(); err != nil {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	exportDOT := flag.String("export-dot", "", "export the module graph as Graphviz DOT and exit")
	exportMD := flag.String("export-md", "", "export the architecture map as Markdown and exit")
	exportHTML := flag.String("export-html", "", "export a self-contained interactive HTML report and exit")
	dbFlag := flag.String("db", "", "SQLite cache path (default: user cache dir; env GO_MCP_DB)")
	dbInfo := flag.Bool("db-info", false, "print populated cache statistics and exit")
	noWatch := flag.Bool("no-watch", false, "disable automatic file watching and reindexing")
	showVersion := flag.Bool("version", false, "print version and exit")
	logLevelFlag := flag.String("log-level", envOr("GO_MCP_LOG_LEVEL", "info"), "log level: off|error|info")
	flag.Parse()

	if *showVersion {
		fmt.Printf("go-mcp-server %s\n", buildVersion())
		return nil
	}

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

	if *dbInfo {
		dbPath, err := cache.ResolvePath(root, *dbFlag)
		if err != nil {
			return err
		}
		summaries, err := cache.Open(dbPath)
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
		fmt.Println(render.JSON(stats))
		return nil
	}

	if *exportDOT != "" || *exportMD != "" || *exportHTML != "" {
		dbPath := ""
		if *exportHTML != "" {
			dbPath, err = cache.ResolvePath(root, *dbFlag)
			if err != nil {
				return err
			}
		}
		return export(root, *exportDOT, *exportMD, *exportHTML, dbPath, logger, formats)
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
	server.SetVersion(buildVersion())
	err = server.Serve(ctx)
	stop()
	return err
}

func export(root, dotPath, mdPath, htmlPath, dbPath string, logger *slog.Logger, formats *render.Registry) error {
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
	if htmlPath != "" {
		html, reportData, err := buildHTML(idx, dbPath)
		if err != nil {
			return err
		}
		if err := writeAtomic(htmlPath, []byte(html)); err != nil {
			return err
		}
		cachedSummaries := 0
		for _, symbol := range reportData.Symbols {
			if symbol.Summary != nil {
				cachedSummaries++
			}
		}
		logger.Info("wrote HTML", "path", htmlPath, "bytes", len(html), "symbols", len(reportData.Symbols), "summaries", cachedSummaries)
	}
	return nil
}

func buildHTML(idx *index.Index, dbPath string) (string, render.HTMLReport, error) {
	summaries, err := cache.Open(dbPath)
	if err != nil {
		return "", render.HTMLReport{}, fmt.Errorf("open summary cache: %w", err)
	}
	reportData, err := report.Build(context.Background(), idx, summaries)
	if err != nil {
		_ = summaries.Close()
		return "", render.HTMLReport{}, err
	}
	html, err := render.RenderHTML(reportData)
	closeErr := summaries.Close()
	if err != nil {
		return "", render.HTMLReport{}, err
	}
	if closeErr != nil {
		return "", render.HTMLReport{}, closeErr
	}
	return html, reportData, nil
}

func writeAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".go-mcp-report-*.html")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
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

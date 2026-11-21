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

	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/mcp"
	"github.com/yanulabs/go-mcp-server/internal/render"
	"github.com/yanulabs/go-mcp-server/internal/tools"
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

	if *exportDOT != "" || *exportMD != "" {
		return export(root, *exportDOT, *exportMD, logger)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := index.NewStore(root, logger)
	store.Start()

	server := mcp.NewServer(os.Stdin, os.Stdout, tools.New(store))
	return server.Serve(ctx)
}

func export(root, dotPath, mdPath string, logger *slog.Logger) error {
	idx, err := index.Build(root)
	if err != nil {
		return err
	}
	if dotPath != "" {
		if err := os.WriteFile(dotPath, []byte(render.CodebaseMapDOT(idx)), 0o644); err != nil {
			return err
		}
		logger.Info("wrote DOT", "path", dotPath, "modules", len(idx.Modules))
	}
	if mdPath != "" {
		if err := os.WriteFile(mdPath, []byte(render.CodebaseMapMarkdown(idx)), 0o644); err != nil {
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

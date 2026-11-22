package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
)

var indexValueFlags = map[string]bool{"db": true, "tags": true}

type indexOptions struct {
	path      string
	dbPath    string
	tags      string
	workspace bool
	json      bool
}

type indexResult struct {
	Root            string   `json:"root"`
	ModulePath      string   `json:"module_path"`
	ModulePaths     []string `json:"module_paths,omitempty"`
	BuildTags       []string `json:"build_tags,omitempty"`
	Workspace       bool     `json:"workspace"`
	BuildMode       string   `json:"build_mode"`
	DBPath          string   `json:"db_path"`
	SummaryDBPath   string   `json:"summary_db_path"`
	Packages        int      `json:"packages"`
	Files           int      `json:"files"`
	Symbols         int      `json:"symbols"`
	Edges           int      `json:"edges"`
	Eligible        int      `json:"eligible"`
	Synced          int      `json:"synced"`
	CoveragePercent float64  `json:"coverage_percent"`
	Warnings        int      `json:"warnings"`
	DurationMS      int64    `json:"duration_ms"`
}

func parseIndexArgs(args []string) (indexOptions, error) {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	quietFlagSet(fs)
	var opts indexOptions
	fs.StringVar(&opts.dbPath, "db", "", "SQLite cache path")
	fs.StringVar(&opts.tags, "tags", "", "Go build tags (comma- or space-separated)")
	fs.BoolVar(&opts.workspace, "workspace", false, "index all modules in the active go.work")
	fs.BoolVar(&opts.json, "json", false, "emit JSON stats")
	positionals, err := parseFlagSet(fs, args)
	if err != nil {
		return indexOptions{}, err
	}
	opts.path, err = singlePath(positionals, "index")
	if err != nil {
		return indexOptions{}, err
	}
	return opts, nil
}

func runIndex(args []string, stdout, stderr io.Writer) error {
	opts, err := parseIndexArgs(args)
	if err != nil {
		return err
	}
	return indexOnce(opts, stdout, stderr)
}

func indexOnce(opts indexOptions, stdout, stderr io.Writer) error {
	start := time.Now()
	root, err := index.ResolveProjectRoot(opts.path, opts.workspace)
	if err != nil {
		return err
	}
	tags := buildTags(opts.tags)
	// Build before opening SQLite. Production load errors must leave any
	// existing healthy working state untouched.
	idx, err := index.BuildWithOptions(root, index.BuildOptions{Tags: tags, Workspace: opts.workspace})
	if err != nil {
		return err
	}
	for _, warning := range idx.TestLoadErrors {
		_, _ = fmt.Fprintf(stderr, "warning: %s\n", warning)
	}

	dbPath, summaryDBPath, err := cache.ResolvePathsWithTags(root, opts.dbPath, tags)
	if err != nil {
		return err
	}
	summaries, err := cache.OpenSplit(dbPath, summaryDBPath)
	if err != nil {
		return fmt.Errorf("open summary cache: %w", err)
	}
	ctx := context.Background()
	if err := summaries.Sync(ctx, idx); err != nil {
		_ = summaries.Close()
		return fmt.Errorf("sync index: %w", err)
	}
	coverage, coverageErr := summaries.Coverage(ctx, "")
	stats, statsErr := summaries.Stats(ctx)
	closeErr := summaries.Close()
	if coverageErr != nil {
		return coverageErr
	}
	if statsErr != nil {
		return statsErr
	}
	if closeErr != nil {
		return closeErr
	}

	files := 0
	for _, symbol := range idx.Summary.Symbols {
		if symbol.Kind == "file" {
			files++
		}
	}
	result := indexResult{
		Root:            root,
		ModulePath:      idx.ModulePath,
		ModulePaths:     idx.ModulePaths,
		BuildTags:       idx.BuildTags,
		Workspace:       idx.Workspace,
		BuildMode:       "full",
		DBPath:          dbPath,
		SummaryDBPath:   summaryDBPath,
		Packages:        len(idx.Packages),
		Files:           files,
		Symbols:         len(idx.Summary.Symbols),
		Edges:           stats.WorkingEdges,
		Eligible:        coverage.Totals.Eligible,
		Synced:          coverage.Totals.Synced,
		CoveragePercent: coverage.Totals.CoveragePct,
		Warnings:        len(idx.TestLoadErrors),
		DurationMS:      time.Since(start).Milliseconds(),
	}

	if opts.json {
		encoder := json.NewEncoder(stdout)
		return encoder.Encode(result)
	}
	return printIndexResult(stdout, result, time.Since(start))
}

func printIndexResult(w io.Writer, result indexResult, elapsed time.Duration) error {
	_, err := fmt.Fprintf(w, `Indexed %s

  Root:      %s
  Database:  %s
  Summaries: %s
  Packages:  %d
  Files:     %d
  Symbols:   %d
  Edges:     %d
  Eligible:  %d
  Coverage:  %.1f%% (%d / %d synced)
`, result.ModulePath, result.Root, result.DBPath, result.SummaryDBPath,
		result.Packages, result.Files, result.Symbols, result.Edges,
		result.Eligible, result.CoveragePercent, result.Synced, result.Eligible)
	if err != nil {
		return err
	}
	if result.Warnings > 0 {
		if _, err := fmt.Fprintf(w, "  Warnings:  %d\n", result.Warnings); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "  Duration:  %s\n", elapsed.Round(time.Millisecond))
	return err
}

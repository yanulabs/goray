package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
	"github.com/yanulabs/goray/internal/report"
)

var exportValueFlags = map[string]bool{"o": true, "out": true, "db": true, "tags": true}

type exportOptions struct {
	format    string
	path      string
	out       string
	dbPath    string
	tags      string
	workspace bool
	open      bool
}

func parseExportArgs(args []string) (exportOptions, error) {
	if len(args) == 0 {
		return exportOptions{}, usagef("export requires a format: html, md, or dot")
	}
	opts := exportOptions{format: args[0]}
	if strings.HasPrefix(opts.format, "-") {
		return exportOptions{}, usagef("export requires a format: html, md, or dot")
	}
	switch opts.format {
	case "html", "md", "dot":
	default:
		return exportOptions{}, usagef("unknown export format %q (use html, md, or dot)", opts.format)
	}

	fs := flag.NewFlagSet("export "+opts.format, flag.ContinueOnError)
	quietFlagSet(fs)
	fs.StringVar(&opts.out, "o", "", "output file")
	fs.StringVar(&opts.out, "out", "", "output file")
	fs.StringVar(&opts.dbPath, "db", "", "SQLite cache path")
	fs.StringVar(&opts.tags, "tags", "", "Go build tags (comma- or space-separated)")
	fs.BoolVar(&opts.workspace, "workspace", false, "index all modules in the active go.work")
	fs.BoolVar(&opts.open, "open", false, "open HTML report")
	positionals, err := parseFlagSet(fs, args[1:])
	if err != nil {
		return exportOptions{}, err
	}
	opts.path, err = singlePath(positionals, "export "+opts.format)
	if err != nil {
		return exportOptions{}, err
	}
	if opts.open && opts.format != "html" {
		return exportOptions{}, usagef("--open is only valid with html exports")
	}
	return opts, nil
}

func runExport(args []string, stdout, stderr io.Writer, opener func(string) error) error {
	opts, err := parseExportArgs(args)
	if err != nil {
		return err
	}
	return export(opts, stdout, stderr, opener)
}

func export(opts exportOptions, stdout, stderr io.Writer, opener func(string) error) error {
	root, err := index.ResolveProjectRoot(opts.path, opts.workspace)
	if err != nil {
		return err
	}
	content, err := renderExport(root, opts)
	if err != nil {
		return err
	}

	if opts.open {
		target := opts.out
		if target == "" {
			target = filepath.Join(os.TempDir(), "goray-"+cache.RepoSlug(root)+"-report.html")
			_, _ = fmt.Fprintln(stderr, target)
		}
		if err := writeReport(target, []byte(content), opts.format); err != nil {
			return err
		}
		if err := opener(target); err != nil {
			return fmt.Errorf("open %s: %w", target, err)
		}
		return nil
	}
	if opts.out != "" {
		return writeReport(opts.out, []byte(content), opts.format)
	}
	_, err = io.WriteString(stdout, content)
	return err
}

func renderExport(root string, opts exportOptions) (string, error) {
	tags := buildTags(opts.tags)
	idx, err := index.BuildWithOptions(root, index.BuildOptions{Tags: tags, Workspace: opts.workspace})
	if err != nil {
		return "", err
	}
	formats := render.NewRegistry()
	pathOpts := cache.PathOptions{Tags: tags, Workspace: opts.workspace}
	switch opts.format {
	case "html":
		workingPath, summaryPath, err := cache.ResolvePathsWithOptions(root, opts.dbPath, pathOpts)
		if err != nil {
			return "", err
		}
		return buildHTML(idx, workingPath, summaryPath)
	case "md":
		base, ok := formats.RenderCodebaseMap("markdown", idx)
		if !ok {
			return "", errors.New("markdown renderer is not registered")
		}
		summaries, err := markdownSummaries(root, idx, opts.dbPath, pathOpts)
		if err != nil {
			return "", err
		}
		return base + summaries, nil
	case "dot":
		content, ok := formats.RenderCodebaseMap("dot", idx)
		if !ok {
			return "", errors.New("dot renderer is not registered")
		}
		return content, nil
	}
	return "", usagef("unknown export format %q (use html, md, or dot)", opts.format)
}

func markdownSummaries(root string, idx *index.Index, dbFlag string, pathOpts cache.PathOptions) (string, error) {
	workingPath, summaryPath, err := cache.ResolvePathsWithOptions(root, dbFlag, pathOpts)
	if err != nil {
		return "", err
	}
	summaries, err := cache.OpenSplit(workingPath, summaryPath)
	if err != nil {
		return "", fmt.Errorf("open summary cache: %w", err)
	}
	section, sectionErr := report.MarkdownSummaries(context.Background(), idx, summaries)
	closeErr := summaries.Close()
	if sectionErr != nil {
		return "", sectionErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return section, nil
}

func buildHTML(idx *index.Index, workingPath, summaryPath string) (string, error) {
	summaries, err := cache.OpenSplit(workingPath, summaryPath)
	if err != nil {
		return "", fmt.Errorf("open summary cache: %w", err)
	}
	reportData, err := report.Build(context.Background(), idx, summaries)
	if err != nil {
		_ = summaries.Close()
		return "", err
	}
	html, err := render.RenderHTML(reportData)
	closeErr := summaries.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	return html, nil
}

func writeReport(path string, data []byte, format string) error {
	if format == "html" {
		return writeAtomic(path, data)
	}
	dir := filepath.Dir(path)
	//nolint:gosec // exported reports are user-facing artifacts and must remain readable.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	//nolint:gosec // exported reports are user-facing artifacts and must remain readable.
	return os.WriteFile(path, data, 0o644)
}

func writeAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	//nolint:gosec // exported reports are user-facing artifacts and must remain readable.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".goray-report-*.html")
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

func openBrowser(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", path)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", path)
	case "windows":
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", "", path)
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
	return cmd.Run()
}

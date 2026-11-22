package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yanulabs/goray/internal/index"
)

var diffValueFlags = map[string]bool{"format": true, "fail-on": true, "tags": true}

type diffOptions struct {
	baseRef   string
	path      string
	format    string
	failOn    []string
	tags      string
	workspace bool
}

func parseDiffArgs(args []string) (diffOptions, error) {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	quietFlagSet(fs)
	opts := diffOptions{baseRef: "HEAD", path: ".", format: "md"}
	fs.StringVar(&opts.format, "format", "md", "output format: md or json")
	var failOn string
	fs.StringVar(&failOn, "fail-on", "", "comma-separated gates: cycles, dead_code, breaking_api, untested_hotspots")
	fs.StringVar(&opts.tags, "tags", "", "Go build tags (comma- or space-separated)")
	fs.BoolVar(&opts.workspace, "workspace", false, "index all modules in the active go.work")
	positionals, err := parseFlagSet(fs, args)
	if err != nil {
		return diffOptions{}, err
	}
	switch opts.format {
	case "md", "json":
	default:
		return diffOptions{}, usagef("unknown diff format %q (use md or json)", opts.format)
	}
	if len(positionals) > 2 {
		return diffOptions{}, usagef("diff accepts at most a base ref and a path")
	}
	if len(positionals) >= 1 && positionals[0] != "" && positionals[0] != "." {
		opts.baseRef = positionals[0]
	}
	if len(positionals) == 2 {
		opts.path = positionals[1]
	} else if len(positionals) == 1 && (positionals[0] == "." || strings.HasPrefix(positionals[0], ".")) {
		opts.path = positionals[0]
	}
	for _, gate := range strings.Split(failOn, ",") {
		gate = strings.TrimSpace(gate)
		if gate == "" {
			continue
		}
		switch gate {
		case "cycles", "dead_code", "breaking_api", "untested_hotspots":
			opts.failOn = append(opts.failOn, gate)
		default:
			return diffOptions{}, usagef("unknown --fail-on gate %q", gate)
		}
	}
	return opts, nil
}

type diffReport struct {
	BaseRef    string       `json:"base_ref"`
	BaseCommit string       `json:"base_commit"`
	HeadRef    string       `json:"head_ref"`
	Head       string       `json:"head"`
	Summary    diffCounters `json:"summary"`
	Cycles     diffCycles   `json:"cycles"`
	API        diffAPI      `json:"breaking_api"`
	Hotspots   diffHotspots `json:"hotspots"`
	Dead       diffDeadCode `json:"dead_code"`
	Failed     []string     `json:"failed_gates,omitempty"`
}

type diffCounters struct {
	Packages         int `json:"packages"`
	PackagesBase     int `json:"packages_base"`
	Files            int `json:"files"`
	FilesBase        int `json:"files_base"`
	LOC              int `json:"loc"`
	LOCBase          int `json:"loc_base"`
	Symbols          int `json:"symbols"`
	SymbolsBase      int `json:"symbols_base"`
	ModuleEdges      int `json:"module_edges"`
	ModuleEdgesBase  int `json:"module_edges_base"`
	UntestedHotspots int `json:"untested_hotspots"`
}

type diffCycles struct {
	New      []string `json:"new"`
	Resolved []string `json:"resolved"`
}

type diffAPI struct {
	Removed  []diffAPIChange `json:"removed"`
	Modified []diffAPIChange `json:"modified"`
	Added    []diffAPIChange `json:"added"`
}

type diffAPIChange struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type diffHotspots struct {
	New      []diffHotspot `json:"new"`
	TopDelta []diffHotspot `json:"top_delta"`
}

type diffHotspot struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Module      string `json:"module"`
	BeforeLOC   int    `json:"before_loc,omitempty"`
	AfterLOC    int    `json:"after_loc"`
	BeforeCC    int    `json:"before_complexity,omitempty"`
	AfterCC     int    `json:"after_complexity"`
	DeltaLOC    int    `json:"delta_loc"`
	DeltaCC     int    `json:"delta_complexity"`
	TestCovered bool   `json:"test_covered"`
}

type diffDeadCode struct {
	New     []diffDeadItem `json:"new"`
	Removed []diffDeadItem `json:"removed"`
}

type diffDeadItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Module string `json:"module"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

func runDiff(args []string, stdout, stderr io.Writer) error {
	opts, err := parseDiffArgs(args)
	if err != nil {
		return err
	}
	report, err := buildDiff(opts, stderr)
	if err != nil {
		return err
	}
	if opts.format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else if err := printDiffMarkdown(stdout, report); err != nil {
		return err
	}
	if len(report.Failed) > 0 {
		return fmt.Errorf("diff gates failed: %s", strings.Join(report.Failed, ", "))
	}
	return nil
}

func buildDiff(opts diffOptions, stderr io.Writer) (diffReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	repo, err := gitOutput(ctx, opts.path, "rev-parse", "--show-toplevel")
	if err != nil {
		return diffReport{}, usagef("not inside a git repository: %v", err)
	}
	repo = strings.TrimSpace(repo)
	baseCommit, err := gitOutput(ctx, repo, "rev-parse", "--verify", opts.baseRef+"^{commit}")
	if err != nil {
		return diffReport{}, usagef("cannot resolve base ref %q: %v", opts.baseRef, err)
	}
	baseCommit = strings.TrimSpace(baseCommit)

	headRoot, err := index.ResolveProjectRoot(opts.path, opts.workspace)
	if err != nil {
		return diffReport{}, err
	}
	rel, err := filepath.Rel(repo, headRoot)
	if err != nil || strings.HasPrefix(rel, "..") {
		return diffReport{}, usagef("indexed path %s is outside git repository %s", headRoot, repo)
	}

	tmpDir, err := os.MkdirTemp("", "goray-diff-*")
	if err != nil {
		return diffReport{}, err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = gitOutput(cleanupCtx, repo, "worktree", "remove", "--force", tmpDir)
		_ = os.RemoveAll(tmpDir)
	}()
	if _, err := gitOutput(ctx, repo, "worktree", "add", "--detach", tmpDir, baseCommit); err != nil {
		return diffReport{}, fmt.Errorf("create base worktree: %w", err)
	}
	baseRoot := filepath.Join(tmpDir, rel)
	if opts.workspace {
		if workspace, ok := index.FindGoWork(baseRoot); ok {
			baseRoot = workspace
		}
	}

	tags := buildTags(opts.tags)
	buildOpts := index.BuildOptions{Tags: tags, Workspace: opts.workspace}
	head, err := index.BuildWithOptions(headRoot, buildOpts)
	if err != nil {
		return diffReport{}, fmt.Errorf("index working tree: %w", err)
	}
	base, err := index.BuildWithOptions(baseRoot, buildOpts)
	if err != nil {
		return diffReport{}, fmt.Errorf("index base ref: %w", err)
	}
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "goray diff: base %s (%d symbols) -> head (%d symbols)\n", opts.baseRef, len(base.Symbols), len(head.Symbols))
	}
	report := compareIndexes(base, head)
	report.BaseRef = opts.baseRef
	report.BaseCommit = baseCommit
	report.HeadRef = "working tree"
	report.Head = baseCommit
	if headCommit, err := gitOutput(ctx, repo, "rev-parse", "--verify", "HEAD^{commit}"); err == nil {
		report.Head = strings.TrimSpace(headCommit)
	}
	for _, gate := range opts.failOn {
		if gateTriggered(report, gate) {
			report.Failed = append(report.Failed, gate)
		}
	}
	return report, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), ctxErr)
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func compareIndexes(base, head *index.Index) diffReport {
	report := diffReport{
		Summary: diffCounters{
			Packages: len(head.Packages), PackagesBase: len(base.Packages),
			Files: indexFileCount(head), FilesBase: indexFileCount(base),
			LOC: indexLOC(head), LOCBase: indexLOC(base),
			Symbols: len(head.Symbols), SymbolsBase: len(base.Symbols),
			ModuleEdges: moduleEdgeCount(head), ModuleEdgesBase: moduleEdgeCount(base),
			UntestedHotspots: untestedHotspotCount(head),
		},
	}

	baseCycles := cycleSet(base)
	headCycles := cycleSet(head)
	for cycle := range headCycles {
		if !baseCycles[cycle] {
			report.Cycles.New = append(report.Cycles.New, cycle)
		}
	}
	for cycle := range baseCycles {
		if !headCycles[cycle] {
			report.Cycles.Resolved = append(report.Cycles.Resolved, cycle)
		}
	}
	sort.Strings(report.Cycles.New)
	sort.Strings(report.Cycles.Resolved)

	baseAPI := exportedAPI(base)
	headAPI := exportedAPI(head)
	for id, after := range headAPI {
		before, ok := baseAPI[id]
		if !ok {
			report.API.Added = append(report.API.Added, after)
			continue
		}
		if before.Before != after.Before {
			changed := after
			changed.Before = before.Before
			report.API.Modified = append(report.API.Modified, changed)
		}
	}
	for id, before := range baseAPI {
		if _, ok := headAPI[id]; !ok {
			report.API.Removed = append(report.API.Removed, before)
		}
	}
	sortAPIChanges(report.API.Added)
	sortAPIChanges(report.API.Modified)
	sortAPIChanges(report.API.Removed)

	headHotspots := complexFunctions(head)
	baseHotspotMap := baseHotspots(base)
	for id, after := range headHotspots {
		if head.TestCovered[id] {
			continue
		}
		before, existed := baseHotspotMap[id]
		if !existed {
			report.Hotspots.New = append(report.Hotspots.New, after)
			continue
		}
		if after.AfterCC != before.AfterCC || after.AfterLOC != before.AfterLOC {
			delta := after
			delta.BeforeCC = before.AfterCC
			delta.BeforeLOC = before.AfterLOC
			delta.DeltaCC = after.AfterCC - before.AfterCC
			delta.DeltaLOC = after.AfterLOC - before.AfterLOC
			report.Hotspots.TopDelta = append(report.Hotspots.TopDelta, delta)
		}
	}
	sort.Slice(report.Hotspots.New, func(i, j int) bool {
		if report.Hotspots.New[i].AfterCC != report.Hotspots.New[j].AfterCC {
			return report.Hotspots.New[i].AfterCC > report.Hotspots.New[j].AfterCC
		}
		return report.Hotspots.New[i].ID < report.Hotspots.New[j].ID
	})
	sort.Slice(report.Hotspots.TopDelta, func(i, j int) bool {
		left := report.Hotspots.TopDelta[i].DeltaCC*1000 + report.Hotspots.TopDelta[i].DeltaLOC
		right := report.Hotspots.TopDelta[j].DeltaCC*1000 + report.Hotspots.TopDelta[j].DeltaLOC
		if left != right {
			return left > right
		}
		return report.Hotspots.TopDelta[i].ID < report.Hotspots.TopDelta[j].ID
	})
	if len(report.Hotspots.TopDelta) > 10 {
		report.Hotspots.TopDelta = report.Hotspots.TopDelta[:10]
	}

	deadOpts := index.DeadCodeOptions{}
	baseDead := deadSet(base.DeadCode(deadOpts))
	headDead := deadSet(head.DeadCode(deadOpts))
	for id, item := range headDead {
		if _, ok := baseDead[id]; !ok {
			report.Dead.New = append(report.Dead.New, item)
		}
	}
	for id, item := range baseDead {
		if _, ok := headDead[id]; !ok {
			report.Dead.Removed = append(report.Dead.Removed, item)
		}
	}
	sortDeadItems(report.Dead.New)
	sortDeadItems(report.Dead.Removed)
	return report
}

func cycleSet(idx *index.Index) map[string]bool {
	out := map[string]bool{}
	for _, cycle := range idx.ModuleCycles() {
		out[strings.Join(cycle, " -> ")] = true
	}
	return out
}

func exportedAPI(idx *index.Index) map[string]diffAPIChange {
	out := map[string]diffAPIChange{}
	for id, sym := range idx.Symbols {
		if sym.IsTest || !sym.Exported {
			continue
		}
		switch sym.Kind {
		case "func", "method", "type", "interface", "struct", "var", "const":
		default:
			continue
		}
		canonical := sym.CanonicalID()
		if canonical == "" {
			canonical = id
		}
		out[canonical] = diffAPIChange{
			ID: canonical, Name: sym.Name, Kind: sym.Kind, File: sym.File, Line: sym.Line,
			Before: sym.Signature,
		}
	}
	return out
}

func complexFunctions(idx *index.Index) map[string]diffHotspot {
	out := map[string]diffHotspot{}
	for id, sym := range idx.Symbols {
		if sym.IsTest || (sym.Kind != "func" && sym.Kind != "method") || sym.Cyclomatic < 10 {
			continue
		}
		out[id] = diffHotspot{
			ID: id, Name: sym.Name, File: sym.File, Line: sym.Line, Module: sym.ModuleArea,
			AfterCC: sym.Cyclomatic, AfterLOC: sym.LOC, TestCovered: idx.TestCovered[id],
		}
	}
	return out
}

func baseHotspots(idx *index.Index) map[string]diffHotspot { return complexFunctions(idx) }

func untestedHotspotCount(idx *index.Index) int {
	count := 0
	for id, sym := range idx.Symbols {
		if sym.IsTest || (sym.Kind != "func" && sym.Kind != "method") || sym.Cyclomatic < 10 {
			continue
		}
		if !idx.TestCovered[id] {
			count++
		}
	}
	return count
}

func deadSet(items []*index.Symbol) map[string]diffDeadItem {
	out := map[string]diffDeadItem{}
	for _, sym := range items {
		out[sym.ID] = diffDeadItem{
			ID: sym.ID, Name: sym.Name, Kind: sym.Kind, Module: sym.ModuleArea,
			File: sym.File, Line: sym.Line,
		}
	}
	return out
}

func indexFileCount(idx *index.Index) int {
	n := 0
	for _, pkg := range idx.Packages {
		n += pkg.Files + pkg.TestFiles
	}
	return n
}

func indexLOC(idx *index.Index) int {
	n := 0
	for _, pkg := range idx.Packages {
		n += pkg.ProdLOC + pkg.TestLOC
	}
	return n
}

func moduleEdgeCount(idx *index.Index) int {
	n := 0
	for _, mod := range idx.Modules {
		n += len(mod.OutModules)
	}
	return n
}

func sortAPIChanges(items []diffAPIChange) {
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
}

func sortDeadItems(items []diffDeadItem) {
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
}

func gateTriggered(report diffReport, gate string) bool {
	switch gate {
	case "cycles":
		return len(report.Cycles.New) > 0
	case "dead_code":
		return len(report.Dead.New) > 0
	case "breaking_api":
		return len(report.API.Removed) > 0 || len(report.API.Modified) > 0
	case "untested_hotspots":
		return len(report.Hotspots.New) > 0 || len(report.Hotspots.TopDelta) > 0
	}
	return false
}

func printDiffMarkdown(w io.Writer, report diffReport) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# goray diff: `%s` vs working tree\n\n", report.BaseRef)
	fmt.Fprintf(&b, "| Metric | Base | Head | Delta |\n|---|---:|---:|---:|\n")
	row := func(name string, before, after int) {
		fmt.Fprintf(&b, "| %s | %d | %d | %+d |\n", name, before, after, after-before)
	}
	row("Packages", report.Summary.PackagesBase, report.Summary.Packages)
	row("Files", report.Summary.FilesBase, report.Summary.Files)
	row("LOC", report.Summary.LOCBase, report.Summary.LOC)
	row("Symbols", report.Summary.SymbolsBase, report.Summary.Symbols)
	row("Module edges", report.Summary.ModuleEdgesBase, report.Summary.ModuleEdges)
	fmt.Fprintf(&b, "| Untested hotspots | - | %d | - |\n", report.Summary.UntestedHotspots)

	fmt.Fprintf(&b, "\n## Cycles\n\n- New: %d\n- Resolved: %d\n", len(report.Cycles.New), len(report.Cycles.Resolved))
	for _, cycle := range report.Cycles.New {
		fmt.Fprintf(&b, "  - `%s`\n", cycle)
	}
	for _, cycle := range report.Cycles.Resolved {
		fmt.Fprintf(&b, "  - resolved `%s`\n", cycle)
	}

	fmt.Fprintf(&b, "\n## Breaking API\n\n- Removed: %d\n- Modified: %d\n- Added: %d\n",
		len(report.API.Removed), len(report.API.Modified), len(report.API.Added))
	for _, change := range report.API.Removed {
		fmt.Fprintf(&b, "  - removed `%s` (%s:%d)\n", change.Name, change.File, change.Line)
	}
	for _, change := range report.API.Modified {
		fmt.Fprintf(&b, "  - modified `%s` (%s:%d): `%s` -> `%s`\n", change.Name, change.File, change.Line, change.Before, change.After)
	}

	fmt.Fprintf(&b, "\n## Hotspots\n\n- New untested complex functions: %d\n", len(report.Hotspots.New))
	for _, hotspot := range report.Hotspots.New {
		fmt.Fprintf(&b, "  - `%s` CC=%d LOC=%d (%s:%d)\n", hotspot.Name, hotspot.AfterCC, hotspot.AfterLOC, hotspot.File, hotspot.Line)
	}
	if len(report.Hotspots.TopDelta) > 0 {
		b.WriteString("\n| Function | CC | LOC | ΔCC | ΔLOC |\n|---|---:|---:|---:|---:|\n")
		for _, hotspot := range report.Hotspots.TopDelta {
			fmt.Fprintf(&b, "| `%s` | %d -> %d | %d -> %d | %+d | %+d |\n",
				hotspot.Name, hotspot.BeforeCC, hotspot.AfterCC, hotspot.BeforeLOC, hotspot.AfterLOC, hotspot.DeltaCC, hotspot.DeltaLOC)
		}
	}

	fmt.Fprintf(&b, "\n## Dead Code\n\n- New: %d\n- Removed: %d\n", len(report.Dead.New), len(report.Dead.Removed))
	for _, item := range report.Dead.New {
		fmt.Fprintf(&b, "  - new `%s` (%s:%d)\n", item.Name, item.File, item.Line)
	}
	for _, item := range report.Dead.Removed {
		fmt.Fprintf(&b, "  - removed `%s` (%s:%d)\n", item.Name, item.File, item.Line)
	}
	if len(report.Failed) > 0 {
		fmt.Fprintf(&b, "\n**Failed gates:** %s\n", strings.Join(report.Failed, ", "))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

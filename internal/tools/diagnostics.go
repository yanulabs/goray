package tools

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

type diagnosticsPayload struct {
	Diagnostics         []index.Diagnostic  `json:"diagnostics"`
	Counts              map[string]int      `json:"counts_by_kind"`
	IndexState          index.State         `json:"index_state"`
	LastHealthySnapshot lastHealthySnapshot `json:"last_healthy_snapshot"`
}

type lastHealthySnapshot struct {
	Available bool   `json:"available"`
	BuiltAt   string `json:"built_at,omitempty"`
	Packages  int    `json:"packages,omitempty"`
	Symbols   int    `json:"symbols,omitempty"`
	BuildMS   int64  `json:"build_ms,omitempty"`
	BuildMode string `json:"build_mode,omitempty"`
}

func (t *toolset) diagnostics() mcp.Tool {
	return mcp.Tool{
		Name:        "get_diagnostics",
		Annotations: readOnlyAnnotations(),
		Description: "Structured package load diagnostics (list/parse/type) with file, line, column, package, and is_test flags. Production errors keep the last healthy index snapshot available. Test-only errors are captured on successful builds.",
		InputSchema: object(map[string]any{
			"package":       propString("Optional package path, relative package path, or module name filter"),
			"include_tests": propBool("Include test-only diagnostics (default: true)"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			idx, waitErr := t.src.Wait(ctx)
			if waitErr != nil {
				if text, ok := indexingPayload(waitErr); ok {
					return text, false
				}
			}
			status := t.src.Status()
			filter := strings.TrimSpace(packageArg(args))
			includeTests := boolArg(args, "include_tests", true)
			counts := map[string]int{
				index.DiagnosticKindList:  0,
				index.DiagnosticKindParse: 0,
				index.DiagnosticKindType:  0,
			}
			diagnostics := make([]index.Diagnostic, 0)
			for _, diagnostic := range t.src.Diagnostics() {
				if !includeTests && diagnostic.IsTest {
					continue
				}
				if !diagnosticMatchesPackage(diagnostic, filter, idx) {
					continue
				}
				diagnostics = append(diagnostics, diagnostic)
				counts[diagnostic.Kind]++
			}
			payload := diagnosticsPayload{
				Diagnostics: diagnostics,
				Counts:      counts,
				IndexState:  status.IndexState,
			}
			payload.Counts["total"] = len(diagnostics)
			payload.LastHealthySnapshot = lastHealthy(status, idx)
			return render.JSON(payload), false
		},
	}
}

func lastHealthy(status index.Status, idx *index.Index) lastHealthySnapshot {
	snapshot := lastHealthySnapshot{
		Available: idx != nil || status.Ready,
		BuildMode: status.LastBuildMode,
	}
	if idx != nil {
		snapshot.Packages = len(idx.Packages)
		snapshot.Symbols = len(idx.Symbols)
		snapshot.BuildMS = idx.BuildDuration.Milliseconds()
		if !idx.BuiltAt.IsZero() {
			snapshot.BuiltAt = idx.BuiltAt.UTC().Format(time.RFC3339Nano)
		}
		return snapshot
	}
	if status.Ready {
		snapshot.Packages = status.Packages
		snapshot.Symbols = status.Symbols
		snapshot.BuildMS = status.BuildMS
	}
	return snapshot
}

func diagnosticMatchesPackage(diagnostic index.Diagnostic, filter string, idx *index.Index) bool {
	if filter == "" {
		return true
	}
	filter = strings.TrimSuffix(filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(filter), "./")), "/")
	if filter == "" {
		return true
	}
	if diagnostic.Package == filter {
		return true
	}
	if idx != nil {
		if path, ok := idx.ResolvePackage(filter); ok {
			if diagnostic.Package == path || diagnostic.Package == path+"_test" {
				return true
			}
		}
	}
	return strings.HasSuffix(diagnostic.Package, "/"+filter)
}

package index

import (
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Diagnostic kinds. They mirror the driver, parser, and type-checker stages of
// go/packages without exposing package-internal ErrorKind values.
const (
	DiagnosticKindList  = "list"
	DiagnosticKindParse = "parse"
	DiagnosticKindType  = "type"
)

// Diagnostic is one structured package load error. File is repository-relative
// when the error position lies inside the indexed root. Line and Col are zero
// when the toolchain did not provide a position (for example driver errors and
// list failures).
type Diagnostic struct {
	Package string `json:"package"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	IsTest  bool   `json:"is_test"`
}

// diagnosticKind maps packages.ErrorKind onto the stable string vocabulary
// exposed by get_diagnostics. UnknownError is reported as a list error because
// it originates in the package driver.
func diagnosticKind(kind packages.ErrorKind) string {
	switch kind {
	case packages.ParseError:
		return DiagnosticKindParse
	case packages.TypeError:
		return DiagnosticKindType
	case packages.UnknownError, packages.ListError:
		return DiagnosticKindList
	default:
		return DiagnosticKindList
	}
}

// newDiagnostic converts one packages.Error into a root-relative Diagnostic.
// The package path comes from the containing package; Pos may be empty, "-", or
// any of file, file:line, and file:line:col.
func newDiagnostic(root, pkgPath string, isTest bool, loadErr packages.Error) Diagnostic {
	file, line, col := parseErrorPosition(loadErr.Pos)
	return Diagnostic{
		Package: pkgPath,
		File:    diagnosticFile(root, file),
		Line:    line,
		Col:     col,
		Kind:    diagnosticKind(loadErr.Kind),
		Message: loadErr.Msg,
		IsTest:  isTest,
	}
}

// parseErrorPosition parses the position formats emitted by go/packages while
// tolerating arbitrary paths (including Windows drive letters) and malformed
// input. It never panics and never treats a non-numeric suffix as a line.
func parseErrorPosition(pos string) (file string, line, col int) {
	pos = strings.TrimSpace(pos)
	if pos == "" || pos == "-" {
		return "", 0, 0
	}
	parts := strings.Split(pos, ":")
	for _, count := range []int{2, 1} {
		if len(parts) <= count {
			continue
		}
		value, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil || value < 0 {
			continue
		}
		if count == 2 {
			previous, err := strconv.Atoi(parts[len(parts)-2])
			if err != nil || previous < 0 {
				continue
			}
			return strings.Join(parts[:len(parts)-2], ":"), previous, value
		}
		return strings.Join(parts[:len(parts)-1], ":"), value, 0
	}
	return pos, 0, 0
}

// diagnosticFile normalizes a toolchain path. Absolute paths inside root become
// slash-separated paths relative to root; paths outside root and already
// relative paths keep their original location semantics.
func diagnosticFile(root, file string) string {
	file = strings.TrimSpace(file)
	if file == "" || file == "-" {
		return ""
	}
	file = filepath.Clean(file)
	if !filepath.IsAbs(file) {
		return filepath.ToSlash(file)
	}
	if root != "" {
		rel, err := filepath.Rel(root, file)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(file)
}

// uniqueDiagnostics removes exact duplicates while preserving first-seen order.
func uniqueDiagnostics(items []Diagnostic) []Diagnostic {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[Diagnostic]struct{}, len(items))
	out := make([]Diagnostic, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// sortDiagnostics orders diagnostics deterministically for index snapshots.
func sortDiagnostics(items []Diagnostic) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Package != items[j].Package {
			return items[i].Package < items[j].Package
		}
		if items[i].File != items[j].File {
			return items[i].File < items[j].File
		}
		if items[i].Line != items[j].Line {
			return items[i].Line < items[j].Line
		}
		if items[i].Col != items[j].Col {
			return items[i].Col < items[j].Col
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Message < items[j].Message
	})
}

// diagnosticsFromError converts a build error into structured diagnostics. Only
// package load errors carry toolchain positions; other build failures become a
// single list diagnostic so get_diagnostics still explains why the snapshot is
// stale.
func diagnosticsFromError(err error) []Diagnostic {
	if err == nil {
		return nil
	}
	var loadErr *LoadError
	if errors.As(err, &loadErr) {
		return uniqueDiagnostics(loadErr.Diagnostics)
	}
	return []Diagnostic{{Kind: DiagnosticKindList, Message: err.Error()}}
}

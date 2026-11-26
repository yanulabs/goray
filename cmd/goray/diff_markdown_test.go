package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/diff"
)

type failingDiffWriter struct{}

func (failingDiffWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestPrintDiffMarkdownSectionsAndWriteErrors(t *testing.T) {
	report := diff.Report{
		BaseRef: "main",
		Summary: diff.Counters{PackagesBase: 1, Packages: 2},
		Cycles:  diff.Cycles{New: []string{"a -> b -> a"}},
		API:     diff.API{Modified: []diff.APIChange{{Name: "Changed", File: "api.go", Line: 3, Before: "int", After: "string"}}},
		Hotspots: diff.Hotspots{
			New:      []diff.Hotspot{{Name: "Complex", AfterCC: 12, AfterLOC: 20, File: "complex.go", Line: 4}},
			TopDelta: []diff.Hotspot{{Name: "Changed", BeforeCC: 10, AfterCC: 11, BeforeLOC: 10, AfterLOC: 12, DeltaCC: 1, DeltaLOC: 2}},
		},
		Dead:   diff.DeadCode{Removed: []diff.DeadItem{{Name: "Gone", File: "dead.go", Line: 5}}},
		Failed: []string{"cycles"},
	}
	var out bytes.Buffer
	if err := printDiffMarkdown(&out, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# goray diff: `main`", "## Cycles", "## Breaking API", "## Hotspots", "## Dead Code", "**Failed gates:** cycles", "ΔCC"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("markdown missing %q:\n%s", want, out.String())
		}
	}
	if err := printDiffMarkdown(failingDiffWriter{}, report); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("write error = %v", err)
	}
}

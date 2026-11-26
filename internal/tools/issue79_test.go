package tools

import (
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/index"
)

func hotspotMarkdownFixture() compactModuleOverview {
	return compactModuleOverview{
		Module: compactModule{Name: "alpha"},
		TopHotspots: []index.Hotspot{
			{
				ID: "example.com/alpha/cache.(*Cache).Sync", Name: "Sync", RecvType: "Cache",
				Kind: "method", File: "cache/sync.go", LOC: 83, Complexity: 15, FanIn: 19, RiskScore: 64,
				Module: "alpha",
			},
			{
				ID: "example.com/alpha.Build", Name: "Build", Kind: "func",
				File: "build.go", LOC: 3, Complexity: 1, FanIn: 59, RiskScore: 7,
				Module: "alpha",
			},
		},
		TotalHotspots: 2,
	}
}

func TestModuleOverviewMarkdownHotspotsIncludeReceiver(t *testing.T) {
	text := compactModuleMarkdown(hotspotMarkdownFixture())
	if !strings.Contains(text, "| 64 | `Cache.Sync` | `cache/sync.go` |") {
		t.Fatalf("method hotspot missing receiver: %s", text)
	}
	if !strings.Contains(text, "| 7 | `Build` | `build.go` |") {
		t.Fatalf("function hotspot name changed: %s", text)
	}
}

func TestCodebaseMapMarkdownHotspotsIncludeReceiver(t *testing.T) {
	out := compactCodebase{
		ModulePath:    "example.com/alpha",
		TopHotspots:   hotspotMarkdownFixture().TopHotspots,
		TotalHotspots: 2,
	}
	text := compactCodebaseMarkdown(out)
	if !strings.Contains(text, "| 64 | `Cache.Sync` | `alpha` | `cache/sync.go` |") {
		t.Fatalf("codebase map method hotspot missing receiver: %s", text)
	}
}

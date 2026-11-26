package index

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func buildCallPathFixture(t *testing.T) *Index {
	t.Helper()
	root := t.TempDir()
	writeTagModule(t, root, "example.com/callpath", map[string]string{
		"app/app.go": `package app

type Store interface {
	Save(amount int) error
}

type DB struct{}

func (DB) Save(amount int) error { return nil }

type Service struct {
	store Store
}

func (s *Service) Run(amount int) error { return s.store.Save(amount) }

func Entry() error { return (&Service{store: DB{}}).Run(1) }

func Target() {}

func Mid() { Target() }

func Top() { Mid() }

func Callback() {}

func Register() { _ = Callback }

func CycleA() { CycleB() }

func CycleB() {
	CycleA()
	Target()
}

func Left() { DiamondTarget() }

func Right() { DiamondTarget() }

func DiamondTarget() {}

func DiamondEntry() {
	Left()
	Right()
}

func Unreachable() {}
`,
		"app/deep.go": callPathDeepSource(),
		"app/fan.go":  callPathFanSource(),
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func callPathDeepSource() string {
	var b strings.Builder
	b.WriteString("package app\n\n")
	for i := 1; i <= 21; i++ {
		fmt.Fprintf(&b, "func Deep%02d() {\n", i)
		if i < 21 {
			fmt.Fprintf(&b, "\tDeep%02d()\n", i+1)
		}
		b.WriteString("}\n\n")
	}
	return b.String()
}

func callPathFanSource() string {
	var b strings.Builder
	b.WriteString("package app\n\nfunc FanEntry() {\n")
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&b, "\tFan%02d()\n", i)
	}
	b.WriteString("}\n\n")
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&b, "func Fan%02d() { FanTarget() }\n\n", i)
	}
	b.WriteString("func FanTarget() {}\n")
	return b.String()
}

type callPathTestCase struct {
	name          string
	from          string
	to            string
	maxDepth      int
	maxPaths      int
	include       bool
	wantFound     bool
	wantPaths     int
	wantDepth     int
	wantEdgeKinds []string
	wantVia       string
	wantReverse   bool
}

func TestFindCallPath(t *testing.T) {
	idx := buildCallPathFixture(t)
	tests := []callPathTestCase{
		{
			name: "direct chain", from: "example.com/callpath/app.Top", to: "example.com/callpath/app.Target",
			include: true, wantFound: true, wantPaths: 1, wantDepth: 2, wantEdgeKinds: []string{"direct", "direct"},
		},
		{
			name: "direct single hop", from: "example.com/callpath/app.Mid", to: "example.com/callpath/app.Target",
			include: true, wantFound: true, wantPaths: 1, wantDepth: 1, wantEdgeKinds: []string{"direct"},
		},
		{
			name: "func value edge", from: "example.com/callpath/app.Register", to: "example.com/callpath/app.Callback",
			include: true, wantFound: true, wantPaths: 1, wantDepth: 1, wantEdgeKinds: []string{"func_value"},
		},
		{
			name: "interface dispatch edge", from: "example.com/callpath/app.Service.Run", to: "example.com/callpath/app.DB.Save",
			include: true, wantFound: true, wantPaths: 1, wantDepth: 1, wantEdgeKinds: []string{"interface_dispatch"},
			wantVia: "example.com/callpath/app.Store",
		},
		{
			name: "interface dispatch disabled", from: "example.com/callpath/app.Entry", to: "example.com/callpath/app.DB.Save",
			include: false, wantFound: false,
		},
		{
			name: "cycle is bounded", from: "example.com/callpath/app.CycleA", to: "example.com/callpath/app.Target",
			include: true, wantFound: true, wantPaths: 1, wantDepth: 2, wantEdgeKinds: []string{"direct", "direct"},
		},
		{
			name: "self target", from: "example.com/callpath/app.Top", to: "example.com/callpath/app.Top",
			include: true, wantFound: true, wantPaths: 1, wantDepth: 0,
		},
		{
			name: "unreachable with reverse path", from: "example.com/callpath/app.Target", to: "example.com/callpath/app.Top",
			include: true, wantFound: false, wantReverse: true,
		},
		{
			name: "unreachable without reverse path", from: "example.com/callpath/app.Unreachable", to: "example.com/callpath/app.Top",
			include: true, wantFound: false,
		},
		{
			name: "multiple shortest paths", from: "example.com/callpath/app.DiamondEntry", to: "example.com/callpath/app.DiamondTarget",
			include: true, wantFound: true, wantPaths: 2, wantDepth: 2, wantEdgeKinds: []string{"direct", "direct"},
		},
		{
			name: "max paths limit", from: "example.com/callpath/app.DiamondEntry", to: "example.com/callpath/app.DiamondTarget",
			maxPaths: 1, include: true, wantFound: true, wantPaths: 1, wantDepth: 2, wantEdgeKinds: []string{"direct", "direct"},
		},
		{
			name: "max depth too small", from: "example.com/callpath/app.Top", to: "example.com/callpath/app.Target",
			maxDepth: 1, include: true, wantFound: false,
		},
		{
			name: "dispatch disabled still follows direct calls", from: "example.com/callpath/app.Top", to: "example.com/callpath/app.Target",
			include: false, wantFound: true, wantPaths: 1, wantDepth: 2, wantEdgeKinds: []string{"direct", "direct"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := idx.FindCallPath(tc.from, tc.to, tc.maxDepth, tc.maxPaths, tc.include)
			if err != nil {
				t.Fatal(err)
			}
			assertCallPathResult(t, tc, result)
		})
	}
}

func assertCallPathResult(t *testing.T, tc callPathTestCase, result CallPathResult) {
	t.Helper()
	if result.Found != tc.wantFound {
		t.Fatalf("found = %v, want %v (%+v)", result.Found, tc.wantFound, result)
	}
	if result.From.ID != tc.from || result.To.ID != tc.to {
		t.Fatalf("endpoints = %s -> %s", result.From.ID, result.To.ID)
	}
	if result.From.File == "" || result.From.Line == 0 || result.To.File == "" || result.To.Line == 0 {
		t.Fatalf("endpoint locations missing: %+v -> %+v", result.From, result.To)
	}
	if result.ReversePathExists != tc.wantReverse {
		t.Fatalf("reverse_path_exists = %v, want %v", result.ReversePathExists, tc.wantReverse)
	}
	if tc.wantReverse && !strings.Contains(result.ReversePathHint, "reverse path") {
		t.Fatalf("reverse hint = %q", result.ReversePathHint)
	}
	if !tc.wantReverse && result.ReversePathHint != "" {
		t.Fatalf("unexpected reverse hint: %q", result.ReversePathHint)
	}
	if !tc.wantFound {
		if len(result.Paths) != 0 {
			t.Fatalf("paths = %+v, want none", result.Paths)
		}
		return
	}
	if len(result.Paths) != tc.wantPaths {
		t.Fatalf("paths = %d, want %d: %+v", len(result.Paths), tc.wantPaths, result.Paths)
	}
	for _, path := range result.Paths {
		assertCallPathShape(t, tc, path)
	}
}

func assertCallPathShape(t *testing.T, tc callPathTestCase, path CallPath) {
	t.Helper()
	if path.Depth != tc.wantDepth || len(path.Hops) != tc.wantDepth || len(path.Nodes) != tc.wantDepth+1 {
		t.Fatalf("path shape = %+v", path)
	}
	if path.Nodes[0].ID != tc.from || path.Nodes[len(path.Nodes)-1].ID != tc.to {
		t.Fatalf("path endpoints = %+v", path.Nodes)
	}
	for _, node := range path.Nodes {
		if node.ID == "" || node.Name == "" || node.Kind == "" || node.File == "" || node.Line == 0 {
			t.Fatalf("incomplete path node: %+v", node)
		}
	}
	for i, hop := range path.Hops {
		if hop.From.ID != path.Nodes[i].ID || hop.To.ID != path.Nodes[i+1].ID {
			t.Fatalf("hop endpoints = %+v", hop)
		}
		if i < len(tc.wantEdgeKinds) && hop.EdgeKind != tc.wantEdgeKinds[i] {
			t.Fatalf("edge kinds = %+v, want %v", path.Hops, tc.wantEdgeKinds)
		}
		if hop.EdgeKind != "interface_dispatch" && hop.ViaInterface != "" {
			t.Fatalf("non-dispatch hop carries via_interface: %+v", hop)
		}
	}
	if tc.wantVia != "" && path.Hops[len(path.Hops)-1].ViaInterface != tc.wantVia {
		t.Fatalf("via_interface = %q, want %q", path.Hops[len(path.Hops)-1].ViaInterface, tc.wantVia)
	}
}

func TestFindCallPathSelfTargetIsZeroHop(t *testing.T) {
	idx := buildCallPathFixture(t)
	result, err := idx.FindCallPath("example.com/callpath/app.Top", "example.com/callpath/app.Top", 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || len(result.Paths) != 1 {
		t.Fatalf("result = %+v", result)
	}
	path := result.Paths[0]
	if path.Depth != 0 || len(path.Hops) != 0 || len(path.Nodes) != 1 || path.Nodes[0].ID != result.From.ID {
		t.Fatalf("self path = %+v", path)
	}
}

func TestFindCallPathClampsLimits(t *testing.T) {
	idx := buildCallPathFixture(t)
	deep := func(n int) string { return fmt.Sprintf("example.com/callpath/app.Deep%02d", n) }

	// An unset max_depth uses the default of 8 hops: the 8-hop boundary is
	// reachable but the 9-hop target is not.
	result, err := idx.FindCallPath(deep(1), deep(9), 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.MaxDepth != 8 || result.MaxPaths != 3 {
		t.Fatalf("default limits = %+v", result)
	}
	result, err = idx.FindCallPath(deep(1), deep(10), 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Found {
		t.Fatalf("default max_depth unexpectedly reached 9 hops: %+v", result)
	}

	// max_depth and max_paths are capped at 20 and 10.
	result, err = idx.FindCallPath(deep(1), deep(21), 100, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.MaxDepth != 20 || result.MaxPaths != 10 || result.Paths[0].Depth != 20 {
		t.Fatalf("clamped limits = %+v", result)
	}
	result, err = idx.FindCallPath(deep(1), deep(21), 19, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Found {
		t.Fatalf("max_depth 19 unexpectedly reached a 20-hop target: %+v", result)
	}

	fanEntry, fanTarget := "example.com/callpath/app.FanEntry", "example.com/callpath/app.FanTarget"
	result, err = idx.FindCallPath(fanEntry, fanTarget, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.MaxPaths != 3 || len(result.Paths) != 3 {
		t.Fatalf("default max_paths = %+v", result)
	}
	result, err = idx.FindCallPath(fanEntry, fanTarget, 0, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.MaxPaths != 10 || len(result.Paths) != 10 {
		t.Fatalf("clamped max_paths = %+v", result)
	}
	if got := result.Paths[0].Nodes[1].Name; got != "Fan00" {
		t.Fatalf("first fan path = %q", got)
	}
}

func TestFindCallPathDeterministic(t *testing.T) {
	idx := buildCallPathFixture(t)
	from, to := "example.com/callpath/app.DiamondEntry", "example.com/callpath/app.DiamondTarget"
	first, err := idx.FindCallPath(from, to, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Paths) != 2 || first.Paths[0].Nodes[1].Name != "Left" || first.Paths[1].Nodes[1].Name != "Right" {
		t.Fatalf("paths are not in stable neighbor order: %+v", first.Paths)
	}
	for i := 0; i < 5; i++ {
		got, err := idx.FindCallPath(from, to, 0, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d differs:\nfirst: %+v\n got: %+v", i, first, got)
		}
	}
}

func TestFindCallPathEndpointErrors(t *testing.T) {
	idx := buildCallPathFixture(t)
	cases := []struct {
		name string
		from string
		to   string
		role string
		kind string
	}{
		{name: "missing from", from: "missing", to: "example.com/callpath/app.Target", role: "from"},
		{name: "missing to", from: "example.com/callpath/app.Top", to: "missing", role: "to"},
		{name: "non-callable from", from: "example.com/callpath/app.Store", to: "example.com/callpath/app.Target", role: "from", kind: "interface"},
		{name: "non-callable to", from: "example.com/callpath/app.Top", to: "example.com/callpath/app.Store", role: "to", kind: "interface"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := idx.FindCallPath(tc.from, tc.to, 0, 0, true)
			var endpoint *CallPathEndpointError
			if !errors.As(err, &endpoint) {
				t.Fatalf("error = %v, want CallPathEndpointError", err)
			}
			if endpoint.Role != tc.role || endpoint.Kind != tc.kind {
				t.Fatalf("error = %+v", endpoint)
			}
		})
	}
}

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/render"
)

type fakeIndexer struct{}

func (fakeIndexer) Wait(context.Context) (*index.Index, error) { return nil, errors.New("not ready") }
func (fakeIndexer) Status() index.Status                       { return index.Status{RootDir: "."} }
func (fakeIndexer) Root() string                               { return "." }
func (fakeIndexer) Rebuild() error                             { return nil }

func TestRegistry(t *testing.T) {
	list := New(fakeIndexer{}, render.NewRegistry())
	if len(list) != 15 {
		t.Fatalf("tools = %d, want 15", len(list))
	}

	seen := map[string]bool{}
	for _, tool := range list {
		if tool.Name == "" || tool.Description == "" || tool.Run == nil {
			t.Fatalf("incomplete tool: %+v", tool)
		}
		if seen[tool.Name] {
			t.Fatalf("duplicate tool %q", tool.Name)
		}
		seen[tool.Name] = true
		if tool.InputSchema["type"] != "object" {
			t.Fatalf("%s has invalid schema: %v", tool.Name, tool.InputSchema)
		}
	}

	text, failed := list[0].Run(context.Background(), nil)
	if failed {
		t.Fatalf("project_info failed: %s", text)
	}
	var info struct {
		Tools int `json:"tools"`
	}
	if err := json.Unmarshal([]byte(text), &info); err != nil || info.Tools != len(list) {
		t.Fatalf("project_info = %s (%v)", text, err)
	}
}

// Package render formats index snapshots as Markdown, Mermaid, DOT, and JSON.
package render

import (
	"encoding/json"
	"sort"

	"github.com/yanulabs/goray/internal/index"
)

// Renderer is the base capability shared by every output format.
type Renderer interface {
	Name() string
}

// CodebaseMapRenderer renders the whole architecture map.
type CodebaseMapRenderer interface {
	Renderer
	RenderCodebaseMap(*index.Index) string
}

// ModuleRenderer renders one module overview.
type ModuleRenderer interface {
	Renderer
	RenderModule(*index.Index, *index.ModSummary) string
}

// GraphRenderer renders a package or call graph.
type GraphRenderer interface {
	Renderer
	RenderGraph(index.Graph) string
}

// baseRenderer is embedded by built-in renderers to share their name.
type baseRenderer struct{ name string }

func (b baseRenderer) Name() string { return b.name }

// Registry resolves renderers by capability and format name.
type Registry struct {
	codebase map[string]CodebaseMapRenderer
	modules  map[string]ModuleRenderer
	graphs   map[string]GraphRenderer
}

// NewRegistry builds the default renderers and appends any custom formats.
// Registering a format name again replaces all of its previous capabilities.
func NewRegistry(extra ...Renderer) *Registry {
	registry := &Registry{
		codebase: map[string]CodebaseMapRenderer{},
		modules:  map[string]ModuleRenderer{},
		graphs:   map[string]GraphRenderer{},
	}
	for _, renderer := range []Renderer{newMarkdown(), newJSON(), newDOT(), newMermaid()} {
		registry.Register(renderer)
	}
	for _, renderer := range extra {
		registry.Register(renderer)
	}
	return registry
}

// Register exposes each capability implemented by the renderer.
func (r *Registry) Register(renderer Renderer) {
	name := renderer.Name()
	delete(r.codebase, name)
	delete(r.modules, name)
	delete(r.graphs, name)
	if capability, ok := renderer.(CodebaseMapRenderer); ok {
		r.codebase[name] = capability
	}
	if capability, ok := renderer.(ModuleRenderer); ok {
		r.modules[name] = capability
	}
	if capability, ok := renderer.(GraphRenderer); ok {
		r.graphs[name] = capability
	}
}

func (r *Registry) RenderCodebaseMap(format string, idx *index.Index) (string, bool) {
	renderer, ok := r.codebase[format]
	if !ok {
		return "", false
	}
	return renderer.RenderCodebaseMap(idx), true
}

func (r *Registry) RenderModule(format string, idx *index.Index, summary *index.ModSummary) (string, bool) {
	renderer, ok := r.modules[format]
	if !ok {
		return "", false
	}
	return renderer.RenderModule(idx, summary), true
}

func (r *Registry) RenderGraph(format string, graph index.Graph) (string, bool) {
	renderer, ok := r.graphs[format]
	if !ok {
		return "", false
	}
	return renderer.RenderGraph(graph), true
}

// CodebaseMapFormats lists formats that can render the architecture map.
func (r *Registry) CodebaseMapFormats() []string { return sortedMapKeys(r.codebase) }

// ModuleFormats lists formats that can render a module overview.
func (r *Registry) ModuleFormats() []string { return sortedMapKeys(r.modules) }

// GraphFormats lists formats that can render graph output.
func (r *Registry) GraphFormats() []string { return sortedMapKeys(r.graphs) }

// JSON returns indented JSON, or a JSON error object if marshaling fails.
func JSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fallback, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(fallback)
	}
	return string(data)
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

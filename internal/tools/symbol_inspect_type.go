package tools

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

type inspectTypeResponse struct {
	index.TypeDetail

	Fields       []index.FieldDetail           `json:"fields,omitempty"`
	Methods      []index.InterfaceMethodDetail `json:"methods,omitempty"`
	MethodSet    index.MethodSetDetail         `json:"method_set"`
	Constructors []index.ConstructorDetail     `json:"constructors,omitempty"`

	Implementations []inspectImplementation `json:"implementations,omitempty"`
	TestMocks       []inspectImplementation `json:"test_mocks,omitempty"`
	Implements      []inspectImplementation `json:"implements,omitempty"`

	Summary     json.RawMessage `json:"summary,omitempty"`
	SyncState   string          `json:"sync_state,omitempty"`
	DepsChanged bool            `json:"deps_changed,omitempty"`
	IndexState  index.State     `json:"index_state"`
}

type inspectImplementation struct {
	index.ImplMatch
	Name      string `json:"name,omitempty"`
	Kind      string `json:"kind,omitempty"`
	PkgPath   string `json:"pkg_path,omitempty"`
	PkgRel    string `json:"pkg_rel,omitempty"`
	Signature string `json:"signature,omitempty"`
	Exported  bool   `json:"exported"`
	Test      bool   `json:"test,omitempty"`
}

func (t *toolset) inspectType() mcp.Tool {
	return mcp.Tool{
		Name:        "inspect_type",
		Annotations: readOnlyAnnotations(),
		Description: "Compiler-exact type inspection for structs, interfaces, named types, and aliases: fields, struct tags, embeddings, promoted members, method sets, constructors, and go/types implementations.",
		InputSchema: object(map[string]any{
			"symbol_id":          propString("Struct, interface, named type, or alias ID, full Go identifier, or repository-relative shorthand"),
			"include_unexported": propBool("Include unexported fields, methods, constructors, and implementations (default: true)"),
			"include_promoted":   propBool("Include fields and methods promoted from embedded types (default: true)"),
		}, "symbol_id"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(symbolArg(args))
			if err != nil {
				return symbolErrorPayload(err), true
			}
			switch sym.Kind {
			case "struct", "interface", "type":
			default:
				return "inspect_type requires a struct, interface, or named/alias type; " + sym.ID + " is a " + sym.Kind, true
			}
			detail, ok := idx.TypeDetails[sym.ID]
			if !ok {
				return "type metadata is unavailable for " + sym.ID + "; run reindex to rebuild the snapshot", true
			}

			includeUnexported := boolArg(args, "include_unexported", true)
			includePromoted := boolArg(args, "include_promoted", true)
			view := filterTypeDetail(idx, detail, includeUnexported, includePromoted)

			summary, state, depsChanged := t.enrich(ctx, sym)
			response := inspectTypeResponse{
				TypeDetail:   view,
				Fields:       view.Fields,
				Methods:      view.Methods,
				MethodSet:    view.MethodSet,
				Constructors: view.Constructors,
				Summary:      summary,
				SyncState:    string(state),
				DepsChanged:  depsChanged,
				IndexState:   t.src.Status().IndexState,
			}
			if sym.Kind == "interface" {
				response.Implementations, response.TestMocks = inspectImplementationMatches(idx, sym.ID, true, includeUnexported)
			} else {
				response.Implements, _ = inspectImplementationMatches(idx, sym.ID, false, includeUnexported)
			}
			return render.JSON(response), false
		}),
	}
}

func filterTypeDetail(idx *index.Index, detail index.TypeDetail, includeUnexported, includePromoted bool) index.TypeDetail {
	out := detail
	out.Fields = nil
	for _, field := range detail.Fields {
		if !includeUnexported && !field.Exported {
			continue
		}
		if !includePromoted && field.PromotedFrom != "" {
			continue
		}
		field.XRefCount = len(idx.XRefs[field.ID])
		out.Fields = append(out.Fields, field)
	}

	out.Methods = nil
	for _, method := range detail.Methods {
		if !includeUnexported && !method.Exported {
			continue
		}
		if !includePromoted && method.EmbeddedFrom != "" {
			continue
		}
		out.Methods = append(out.Methods, method)
	}

	out.EmbeddedInterfaces = nil
	for _, embedded := range detail.EmbeddedInterfaces {
		if !includeUnexported && !embedded.Exported {
			continue
		}
		out.EmbeddedInterfaces = append(out.EmbeddedInterfaces, embedded)
	}

	out.MethodSet = index.MethodSetDetail{
		Value:   filterMethodDetails(detail.MethodSet.Value, includeUnexported, includePromoted),
		Pointer: filterMethodDetails(detail.MethodSet.Pointer, includeUnexported, includePromoted),
	}
	out.Constructors = nil
	for _, constructor := range detail.Constructors {
		if !includeUnexported && !constructor.Exported {
			continue
		}
		out.Constructors = append(out.Constructors, constructor)
	}
	return out
}

func filterMethodDetails(methods []index.MethodDetail, includeUnexported, includePromoted bool) []index.MethodDetail {
	out := make([]index.MethodDetail, 0, len(methods))
	for _, method := range methods {
		if !includeUnexported && !method.Exported {
			continue
		}
		if !includePromoted && method.PromotedFrom != "" {
			continue
		}
		out = append(out, method)
	}
	return out
}

// inspectImplementationMatches returns production and test matches for an
// interface, or all implemented interfaces for a concrete type.
func inspectImplementationMatches(idx *index.Index, id string, interfaceTarget, includeUnexported bool) (production, testMocks []inspectImplementation) {
	matches := idx.TypeImpls[id]
	if interfaceTarget {
		matches = idx.IfaceImpls[id]
	}
	production = make([]inspectImplementation, 0, len(matches))
	testMocks = make([]inspectImplementation, 0)
	for _, match := range matches {
		targetID := match.IfaceID
		if interfaceTarget {
			targetID = match.TypeID
		}
		sym := idx.Symbols[targetID]
		if !includeUnexported && sym != nil && !sym.Exported {
			continue
		}
		entry := inspectImplementation{ImplMatch: match}
		if sym != nil {
			entry.Name = sym.Name
			entry.Kind = sym.Kind
			entry.PkgPath = sym.PkgPath
			entry.PkgRel = sym.PkgRel
			entry.Signature = sym.Signature
			entry.Exported = sym.Exported
			entry.Test = sym.IsTest
		}
		if interfaceTarget && entry.Test {
			testMocks = append(testMocks, entry)
			continue
		}
		production = append(production, entry)
	}
	sort.Slice(production, func(i, j int) bool {
		if production[i].Name != production[j].Name {
			return production[i].Name < production[j].Name
		}
		return production[i].ID() < production[j].ID()
	})
	sort.Slice(testMocks, func(i, j int) bool {
		if testMocks[i].Name != testMocks[j].Name {
			return testMocks[i].Name < testMocks[j].Name
		}
		return testMocks[i].ID() < testMocks[j].ID()
	})
	return production, testMocks
}

func (i inspectImplementation) ID() string {
	if i.TypeID != "" {
		return i.TypeID
	}
	return i.IfaceID
}

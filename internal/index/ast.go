package index

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
)

// moduleArea maps modules/<name>[/...] to <name> and reports whether the
// package is the module's root package.
func moduleArea(relPkg string) (name string, root bool) {
	parts := strings.Split(relPkg, "/")
	if len(parts) >= 2 && parts[0] == "modules" {
		return parts[1], len(parts) == 2
	}
	return "", false
}

func isModuleRoot(relPkg string) bool {
	_, root := moduleArea(relPkg)
	return root
}

func symbolID(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	if fn, ok := obj.(*types.Func); ok {
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			if recv := recvTypeName(sig.Recv().Type()); recv != "" {
				return fn.Pkg().Path() + "." + recv + "." + fn.Name()
			}
		}
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

func recvTypeName(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	switch named := t.(type) {
	case *types.Named:
		if named.Obj() != nil {
			return named.Obj().Name()
		}
	case *types.Alias:
		if named.Obj() != nil {
			return named.Obj().Name()
		}
	}
	return ""
}

func calledIdent(expr ast.Expr) *ast.Ident {
	for {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.Ident:
			return e
		case *ast.SelectorExpr:
			return e.Sel
		default:
			return nil
		}
	}
}

func cyclomatic(body *ast.BlockStmt) int {
	if body == nil {
		return 1
	}
	n := 1
	ast.Inspect(body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			n++
		case *ast.CaseClause:
			if x.List != nil {
				n++
			}
		case *ast.CommClause:
			if x.Comm != nil {
				n++
			}
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				n++
			}
		}
		return true
	})
	return n
}

func qualifier(curPkg string) types.Qualifier {
	return func(other *types.Package) string {
		if other == nil || other.Path() == curPkg {
			return ""
		}
		return other.Name()
	}
}

func typeDesc(tn *types.TypeName) string {
	switch u := tn.Type().Underlying().(type) {
	case *types.Interface:
		methods := make([]string, 0, u.NumMethods())
		for i := 0; i < u.NumMethods(); i++ {
			methods = append(methods, u.Method(i).Name())
		}
		return "interface { " + strings.Join(methods, "; ") + " }"
	case *types.Struct:
		return fmt.Sprintf("struct (%d fields)", u.NumFields())
	default:
		return types.TypeString(u, qualifier(tn.Pkg().Path()))
	}
}

var stdHooks = map[string]bool{
	"Error": true, "String": true, "GoString": true, "ServeHTTP": true,
	"MarshalJSON": true, "UnmarshalJSON": true, "MarshalText": true,
	"UnmarshalText": true, "Scan": true, "Value": true, "Close": true,
	"Read": true, "Write": true, "Format": true, "Unwrap": true,
	"Is": true, "As": true,
}

func isStdHook(name string) bool { return stdHooks[name] }

func isGenerated(name string, src []byte) bool {
	if strings.HasSuffix(name, ".gen.go") || strings.HasSuffix(name, "_gen.go") {
		return true
	}
	head := src
	if len(head) > 512 {
		head = head[:512]
	}
	return bytes.Contains(head, []byte("Code generated")) && bytes.Contains(head, []byte("DO NOT EDIT"))
}

func countLines(src []byte) int {
	if len(src) == 0 {
		return 0
	}
	n := bytes.Count(src, []byte{'\n'})
	if src[len(src)-1] != '\n' {
		n++
	}
	return n
}

func relFile(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func addPair(m map[string]map[string]struct{}, k, v string) {
	if m[k] == nil {
		m[k] = map[string]struct{}{}
	}
	m[k][v] = struct{}{}
}

func addNested(m map[string]map[string]map[string]struct{}, k1, k2, v string) {
	if m[k1] == nil {
		m[k1] = map[string]map[string]struct{}{}
	}
	if m[k1][k2] == nil {
		m[k1][k2] = map[string]struct{}{}
	}
	m[k1][k2][v] = struct{}{}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortSymbols(symbols []*Symbol) {
	sort.Slice(symbols, func(i, j int) bool { return symbols[i].ID < symbols[j].ID })
}

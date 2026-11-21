package index

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func hashParts(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func contentHash(self, dep string) string {
	return hashText(self + "\n" + dep)
}

func hashSorted(lines []string) string {
	sort.Strings(lines)
	return hashParts(lines...)
}

func printNode(node ast.Node) string {
	if node == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, token.NewFileSet(), node); err != nil {
		return ""
	}
	return buf.String()
}

func normalizeDoc(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isDirective(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "//go:") ||
		strings.HasPrefix(line, "//nolint") ||
		strings.HasPrefix(line, "// +build")
}

func directiveLines(groups []*ast.CommentGroup) []string {
	seen := map[string]struct{}{}
	for _, group := range groups {
		if group == nil {
			continue
		}
		for _, comment := range group.List {
			line := strings.TrimSpace(comment.Text)
			if isDirective(line) {
				seen[line] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for line := range seen {
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

func nodeDirectives(file *ast.File, node ast.Node, docs ...*ast.CommentGroup) []string {
	if file == nil || node == nil {
		return nil
	}
	groups := make([]*ast.CommentGroup, 0, len(docs)+2)
	groups = append(groups, docs...)
	for _, group := range file.Comments {
		if group.Pos() >= node.Pos() && group.End() <= node.End() {
			groups = append(groups, group)
		}
	}
	return directiveLines(groups)
}

func (b *builder) canonicalType(t types.Type) string {
	var sb strings.Builder
	b.writeCanonicalType(&sb, t, map[types.Type]bool{})
	return sb.String()
}

func (b *builder) writeCanonicalType(sb *strings.Builder, t types.Type, seen map[types.Type]bool) {
	switch typ := t.(type) {
	case nil:
		sb.WriteString("<nil>")
	case *types.Basic:
		sb.WriteString(typ.Name())
	case *types.Named:
		sb.WriteString(typeObjectName(typ.Obj()))
		b.writeTypeArgs(sb, typ.TypeArgs(), seen)
		if obj := typ.Obj(); obj != nil && obj.Pkg() != nil && b.inModule(obj.Pkg().Path()) && !seen[typ] {
			seen[typ] = true
			sb.WriteString("{")
			b.writeCanonicalType(sb, typ.Underlying(), seen)
			sb.WriteString("}")
			delete(seen, typ)
		}
	case *types.Alias:
		sb.WriteString(typeObjectName(typ.Obj()))
		b.writeTypeArgs(sb, typ.TypeArgs(), seen)
		if obj := typ.Obj(); obj != nil && obj.Pkg() != nil && b.inModule(obj.Pkg().Path()) && !seen[typ] {
			seen[typ] = true
			sb.WriteString("{")
			b.writeCanonicalType(sb, typ.Underlying(), seen)
			sb.WriteString("}")
			delete(seen, typ)
		}
	case *types.Pointer:
		sb.WriteString("*")
		b.writeCanonicalType(sb, typ.Elem(), seen)
	case *types.Slice:
		sb.WriteString("[]")
		b.writeCanonicalType(sb, typ.Elem(), seen)
	case *types.Array:
		fmt.Fprintf(sb, "[%d]", typ.Len())
		b.writeCanonicalType(sb, typ.Elem(), seen)
	case *types.Map:
		sb.WriteString("map[")
		b.writeCanonicalType(sb, typ.Key(), seen)
		sb.WriteString("]")
		b.writeCanonicalType(sb, typ.Elem(), seen)
	case *types.Chan:
		switch typ.Dir() {
		case types.SendOnly:
			sb.WriteString("chan<- ")
		case types.RecvOnly:
			sb.WriteString("<-chan ")
		default:
			sb.WriteString("chan ")
		}
		b.writeCanonicalType(sb, typ.Elem(), seen)
	case *types.Signature:
		if typ.Recv() != nil {
			sb.WriteString("recv(")
			b.writeCanonicalType(sb, typ.Recv().Type(), seen)
			sb.WriteString(") ")
		}
		if params := typ.TypeParams(); params != nil && params.Len() > 0 {
			sb.WriteString("[")
			for i := 0; i < params.Len(); i++ {
				if i > 0 {
					sb.WriteString(", ")
				}
				b.writeCanonicalType(sb, params.At(i), seen)
			}
			sb.WriteString("]")
		}
		sb.WriteString("func(")
		b.writeParams(sb, typ.Params(), typ.Variadic(), seen)
		sb.WriteString(") (")
		b.writeTuple(sb, typ.Results(), seen)
		sb.WriteString(")")
	case *types.Struct:
		sb.WriteString("struct{")
		for i := 0; i < typ.NumFields(); i++ {
			if i > 0 {
				sb.WriteString("; ")
			}
			field := typ.Field(i)
			if field.Embedded() {
				sb.WriteString("embedded ")
			} else {
				sb.WriteString(field.Name())
				sb.WriteString(" ")
			}
			b.writeCanonicalType(sb, field.Type(), seen)
			if tag := typ.Tag(i); tag != "" {
				fmt.Fprintf(sb, " %q", tag)
			}
		}
		sb.WriteString("}")
	case *types.Interface:
		sb.WriteString("interface{")
		for i := 0; i < typ.NumMethods(); i++ {
			if i > 0 {
				sb.WriteString("; ")
			}
			method := typ.Method(i)
			sb.WriteString(method.Name())
			b.writeCanonicalType(sb, method.Type(), seen)
		}
		for i := 0; i < typ.NumEmbeddeds(); i++ {
			if typ.NumMethods() > 0 || i > 0 {
				sb.WriteString("; ")
			}
			b.writeCanonicalType(sb, typ.EmbeddedType(i), seen)
		}
		sb.WriteString("}")
	case *types.Tuple:
		b.writeTuple(sb, typ, seen)
	case *types.TypeParam:
		sb.WriteString(typ.Obj().Name())
		if typ.Constraint() != nil && !seen[typ] {
			seen[typ] = true
			sb.WriteString(" ")
			b.writeCanonicalType(sb, typ.Constraint(), seen)
			delete(seen, typ)
		}
	case *types.Union:
		for i := 0; i < typ.Len(); i++ {
			if i > 0 {
				sb.WriteString("|")
			}
			term := typ.Term(i)
			if term.Tilde() {
				sb.WriteString("~")
			}
			b.writeCanonicalType(sb, term.Type(), seen)
		}
	default:
		sb.WriteString(types.TypeString(t, func(pkg *types.Package) string {
			if pkg == nil {
				return ""
			}
			return pkg.Path()
		}))
	}
}

func (b *builder) writeTypeArgs(sb *strings.Builder, args *types.TypeList, seen map[types.Type]bool) {
	if args == nil || args.Len() == 0 {
		return
	}
	sb.WriteString("[")
	for i := 0; i < args.Len(); i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		b.writeCanonicalType(sb, args.At(i), seen)
	}
	sb.WriteString("]")
}

func (b *builder) writeTuple(sb *strings.Builder, tuple *types.Tuple, seen map[types.Type]bool) {
	if tuple == nil {
		return
	}
	for i := 0; i < tuple.Len(); i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		b.writeCanonicalType(sb, tuple.At(i).Type(), seen)
	}
}

func (b *builder) writeParams(sb *strings.Builder, tuple *types.Tuple, variadic bool, seen map[types.Type]bool) {
	if tuple == nil {
		return
	}
	for i := 0; i < tuple.Len(); i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		typ := tuple.At(i).Type()
		if variadic && i == tuple.Len()-1 {
			if slice, ok := typ.(*types.Slice); ok {
				sb.WriteString("...")
				b.writeCanonicalType(sb, slice.Elem(), seen)
				continue
			}
		}
		b.writeCanonicalType(sb, typ, seen)
	}
}

func typeObjectName(obj *types.TypeName) string {
	if obj == nil {
		return "<nil>"
	}
	if obj.Pkg() == nil {
		return obj.Name()
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

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
	b.writeCanonicalType(&sb, t, map[types.Type]int{})
	return sb.String()
}

func (b *builder) writeCanonicalType(sb *strings.Builder, t types.Type, active map[types.Type]int) {
	if t == nil {
		sb.WriteString("<nil>")
		return
	}
	if depth, ok := active[t]; ok {
		fmt.Fprintf(sb, "cycle(%d)", depth)
		return
	}
	depth := len(active)
	active[t] = depth
	defer delete(active, t)

	switch typ := t.(type) {
	case *types.Basic:
		sb.WriteString(typ.Name())
	case *types.Named:
		b.writeNamedType(sb, typ.Obj(), typ.TypeArgs(), typ.Underlying(), active)
	case *types.Alias:
		b.writeNamedType(sb, typ.Obj(), typ.TypeArgs(), typ.Underlying(), active)
	case *types.Pointer:
		sb.WriteString("*")
		b.writeCanonicalType(sb, typ.Elem(), active)
	case *types.Slice:
		sb.WriteString("[]")
		b.writeCanonicalType(sb, typ.Elem(), active)
	case *types.Array:
		fmt.Fprintf(sb, "[%d]", typ.Len())
		b.writeCanonicalType(sb, typ.Elem(), active)
	case *types.Map:
		sb.WriteString("map[")
		b.writeCanonicalType(sb, typ.Key(), active)
		sb.WriteString("]")
		b.writeCanonicalType(sb, typ.Elem(), active)
	case *types.Chan:
		b.writeChanType(sb, typ, active)
	default:
		b.writeCanonicalComposite(sb, t, active)
	}
}

func (b *builder) writeCanonicalComposite(sb *strings.Builder, t types.Type, active map[types.Type]int) {
	switch typ := t.(type) {
	case *types.Signature:
		b.writeSignatureType(sb, typ, active, true)
	case *types.Struct:
		b.writeStructType(sb, typ, active)
	case *types.Interface:
		b.writeInterfaceType(sb, typ, active)
	case *types.Tuple:
		b.writeTuple(sb, typ, active)
	case *types.TypeParam:
		b.writeTypeParamType(sb, typ, active)
	case *types.Union:
		b.writeUnionType(sb, typ, active)
	default:
		b.writeFallbackType(sb, t)
	}
}

func (b *builder) writeNamedType(sb *strings.Builder, obj *types.TypeName, args *types.TypeList, underlying types.Type, active map[types.Type]int) {
	sb.WriteString(typeObjectName(obj))
	b.writeTypeArgs(sb, args, active)
	if obj == nil || obj.Pkg() == nil || !b.inModule(obj.Pkg().Path()) {
		return
	}
	sb.WriteString("{")
	b.writeCanonicalType(sb, underlying, active)
	sb.WriteString("}")
}

func (b *builder) writeChanType(sb *strings.Builder, typ *types.Chan, active map[types.Type]int) {
	switch typ.Dir() {
	case types.SendOnly:
		sb.WriteString("chan<- ")
	case types.RecvOnly:
		sb.WriteString("<-chan ")
	default:
		sb.WriteString("chan ")
	}
	b.writeCanonicalType(sb, typ.Elem(), active)
}

func (b *builder) writeSignatureType(sb *strings.Builder, typ *types.Signature, active map[types.Type]int, includeRecv bool) {
	if includeRecv && typ.Recv() != nil {
		sb.WriteString("recv(")
		b.writeCanonicalType(sb, typ.Recv().Type(), active)
		sb.WriteString(") ")
	}
	b.writeSignatureTypeParams(sb, typ.TypeParams(), active)
	sb.WriteString("func(")
	b.writeParams(sb, typ.Params(), typ.Variadic(), active)
	sb.WriteString(") (")
	b.writeTuple(sb, typ.Results(), active)
	sb.WriteString(")")
}

func (b *builder) writeSignatureTypeParams(sb *strings.Builder, params *types.TypeParamList, active map[types.Type]int) {
	if params == nil || params.Len() == 0 {
		return
	}
	sb.WriteString("[")
	for i := 0; i < params.Len(); i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		b.writeCanonicalType(sb, params.At(i), active)
	}
	sb.WriteString("]")
}

func (b *builder) writeStructType(sb *strings.Builder, typ *types.Struct, active map[types.Type]int) {
	sb.WriteString("struct{")
	for i := 0; i < typ.NumFields(); i++ {
		if i > 0 {
			sb.WriteString("; ")
		}
		b.writeStructField(sb, typ, i, active)
	}
	sb.WriteString("}")
}

func (b *builder) writeStructField(sb *strings.Builder, typ *types.Struct, i int, active map[types.Type]int) {
	field := typ.Field(i)
	if field.Embedded() {
		sb.WriteString("embedded ")
	} else {
		sb.WriteString(field.Name())
		sb.WriteString(" ")
	}
	b.writeCanonicalType(sb, field.Type(), active)
	if tag := typ.Tag(i); tag != "" {
		fmt.Fprintf(sb, " %q", tag)
	}
}

func (b *builder) writeInterfaceType(sb *strings.Builder, typ *types.Interface, active map[types.Type]int) {
	sb.WriteString("interface{")
	for i := 0; i < typ.NumMethods(); i++ {
		if i > 0 {
			sb.WriteString("; ")
		}
		method := typ.Method(i)
		sb.WriteString(method.Name())
		if sig, ok := method.Type().(*types.Signature); ok {
			b.writeInterfaceMethod(sb, sig, active)
		} else {
			b.writeCanonicalType(sb, method.Type(), active)
		}
	}
	for i := 0; i < typ.NumEmbeddeds(); i++ {
		if typ.NumMethods() > 0 || i > 0 {
			sb.WriteString("; ")
		}
		b.writeCanonicalType(sb, typ.EmbeddedType(i), active)
	}
	sb.WriteString("}")
}

func (b *builder) writeInterfaceMethod(sb *strings.Builder, sig *types.Signature, active map[types.Type]int) {
	if depth, ok := active[sig]; ok {
		fmt.Fprintf(sb, "cycle(%d)", depth)
		return
	}
	depth := len(active)
	active[sig] = depth
	defer delete(active, sig)
	b.writeSignatureType(sb, sig, active, false)
}

func (b *builder) writeTypeParamType(sb *strings.Builder, typ *types.TypeParam, active map[types.Type]int) {
	sb.WriteString(typ.Obj().Name())
	if typ.Constraint() != nil {
		sb.WriteString(" ")
		b.writeCanonicalType(sb, typ.Constraint(), active)
	}
}

func (b *builder) writeUnionType(sb *strings.Builder, typ *types.Union, active map[types.Type]int) {
	for i := 0; i < typ.Len(); i++ {
		if i > 0 {
			sb.WriteString("|")
		}
		term := typ.Term(i)
		if term.Tilde() {
			sb.WriteString("~")
		}
		b.writeCanonicalType(sb, term.Type(), active)
	}
}

func (b *builder) writeFallbackType(sb *strings.Builder, t types.Type) {
	sb.WriteString(types.TypeString(t, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	}))
}

func (b *builder) writeTypeArgs(sb *strings.Builder, args *types.TypeList, active map[types.Type]int) {
	if args == nil || args.Len() == 0 {
		return
	}
	sb.WriteString("[")
	for i := 0; i < args.Len(); i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		b.writeCanonicalType(sb, args.At(i), active)
	}
	sb.WriteString("]")
}

func (b *builder) writeTuple(sb *strings.Builder, tuple *types.Tuple, active map[types.Type]int) {
	if tuple == nil {
		return
	}
	for i := 0; i < tuple.Len(); i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		b.writeCanonicalType(sb, tuple.At(i).Type(), active)
	}
}

func (b *builder) writeParams(sb *strings.Builder, tuple *types.Tuple, variadic bool, active map[types.Type]int) {
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
				b.writeCanonicalType(sb, slice.Elem(), active)
				continue
			}
		}
		b.writeCanonicalType(sb, typ, active)
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

package index

import (
	"go/token"
	"go/types"
	"testing"
)

func TestRegisterInterfaceMethodsNilPackage(t *testing.T) {
	sig := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	method := types.NewFunc(token.NoPos, nil, "Ping", sig)
	iface := types.NewInterfaceType([]*types.Func{method}, nil)
	iface.Complete()
	b := &builder{
		ifaceOwner:  map[types.Object]string{},
		ifaceMethod: map[string][]string{},
	}
	b.registerInterfaceMethods("example.com/inline.Inline", iface)
	if got := b.ifaceOwner[method]; got != "example.com/inline.Inline" {
		t.Fatalf("owner = %q", got)
	}
	if got := b.ifaceMethod[".Ping"]; len(got) != 1 {
		t.Fatalf("fallback key = %v", b.ifaceMethod)
	}
}

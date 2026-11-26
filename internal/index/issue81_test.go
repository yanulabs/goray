package index

import "testing"

func TestSingleSymbolAcceptsSymPrefixForNonCanonicalKinds(t *testing.T) {
	dir := writeGoModule(t, map[string]string{
		"go.mod": "module example.com/issue81\n\ngo 1.23\n",
		"pkg/pkg.go": `package pkg

const Answer = 42

var ErrInvalidSlot = "invalid"

type Config struct {
	Name string
}
`,
	})
	idx, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{
		"example.com/issue81/pkg.Answer",
		"example.com/issue81/pkg.ErrInvalidSlot",
		"example.com/issue81/pkg.Config.Name",
	} {
		sym, err := idx.SingleSymbol("sym:" + id)
		if err != nil {
			t.Fatalf("SingleSymbol(sym:%s) failed: %v", id, err)
		}
		if sym.ID != id {
			t.Fatalf("SingleSymbol(sym:%s) = %s, want %s", id, sym.ID, id)
		}
	}

	if _, err := idx.SingleSymbol("sym:example.com/issue81/pkg.Missing"); err == nil {
		t.Fatal("missing symbol resolved unexpectedly")
	}
}

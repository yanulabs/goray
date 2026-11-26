package index

import "testing"

func TestUnexportedInterfaceMethodsResolveImplementations(t *testing.T) {
	dir := writeGoModule(t, map[string]string{
		"go.mod": "module example.com/issue76\n\ngo 1.23\n",
		"store/store.go": `package store

type Store interface {
	Save() error
	profile(id int) string
}

type memoryStore struct{}

func (*memoryStore) Save() error              { return nil }
func (*memoryStore) profile(id int) string    { return "" }

var _ Store = (*memoryStore)(nil)
`,
	})
	idx, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	const (
		ifaceID   = "example.com/issue76/store.Store"
		typeID    = "example.com/issue76/store.memoryStore"
		saveID    = "example.com/issue76/store.memoryStore.Save"
		profileID = "example.com/issue76/store.memoryStore.profile"
	)
	matches := idx.IfaceImpls[ifaceID]
	var match *ImplMatch
	for i := range matches {
		if matches[i].TypeID == typeID {
			match = &matches[i]
			break
		}
	}
	if match == nil {
		t.Fatalf("%s missing from interface matrix: %+v", typeID, matches)
	}
	if got := match.MethodIDs["Save"]; got != saveID {
		t.Fatalf("Save method mapping = %q, want %q", got, saveID)
	}
	if got := match.MethodIDs["profile"]; got != profileID {
		t.Fatalf("profile method mapping = %q, want %q", got, profileID)
	}
	if !idx.IfaceMethods[profileID] {
		t.Fatalf("unexported interface method %s not marked as an interface hook", profileID)
	}

	for _, sym := range idx.DeadCode(DeadCodeOptions{IncludeExported: true}) {
		if sym.ID == profileID || sym.ID == saveID {
			t.Fatalf("interface implementation %s reported as dead code", sym.ID)
		}
	}
}

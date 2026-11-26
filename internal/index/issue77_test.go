package index

import (
	"strings"
	"testing"
)

func TestInterfaceTypeDescriptorIncludesMethodNames(t *testing.T) {
	dir := writeGoModule(t, map[string]string{
		"go.mod": "module example.com/issue77\n\ngo 1.23\n",
		"reconcile/reconcile.go": `package reconcile

type reconciler interface {
	reconcileAttempts() int
	reconcileBookingID() string
}
`,
	})
	idx, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	sig := idx.Symbols["example.com/issue77/reconcile.reconciler"].Signature
	for _, want := range []string{"reconcileAttempts() int", "reconcileBookingID() string"} {
		if !strings.Contains(sig, want) {
			t.Fatalf("interface descriptor %q missing %q", sig, want)
		}
	}
	if strings.Contains(sig, "func()") {
		t.Fatalf("interface descriptor still strips method names: %q", sig)
	}
}

package payments

import "testing"

func TestServiceCharge(t *testing.T) {
	if err := NewService().Charge(1); err != nil {
		t.Fatal(err)
	}
}

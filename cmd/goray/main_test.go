package main

import "testing"

func TestBuildVersion(t *testing.T) {
	original := version
	defer func() { version = original }()

	version = "v1.2.3"
	if got := buildVersion(); got != "v1.2.3" {
		t.Fatalf("buildVersion = %q", got)
	}

	version = "dev"
	if got := buildVersion(); got == "" {
		t.Fatal("buildVersion must never be empty")
	}
}

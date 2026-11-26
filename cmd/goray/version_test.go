package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func setTestVersion(t *testing.T, value, revision, built string) {
	t.Helper()
	oldVersion, oldCommit, oldDate := version, commit, buildDate
	version, commit, buildDate = value, revision, built
	t.Cleanup(func() {
		version, commit, buildDate = oldVersion, oldCommit, oldDate
	})
}

func TestVersionHumanOutput(t *testing.T) {
	setTestVersion(t, "v1.2.3", "1234567890abcdef", "2026-11-26T12:00:00Z")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"version"}, &stdout, &stderr, nil); code != 0 {
		t.Fatalf("version exit=%d stderr=%s", code, stderr.String())
	}
	want := "goray v1.2.3\n" +
		"commit: 1234567\n" +
		"built: 2026-11-26T12:00:00Z\n" +
		"go: " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH + "\n"
	if got := stdout.String(); got != want {
		t.Fatalf("version output:\n%s\nwant:\n%s", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("version stderr = %q", stderr.String())
	}
}

func TestVersionShortOutput(t *testing.T) {
	setTestVersion(t, "v1.2.3", "1234567890abcdef", "2026-11-26T12:00:00Z")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"version", "--short"}, &stdout, &stderr, nil); code != 0 {
		t.Fatalf("version --short exit=%d stderr=%s", code, stderr.String())
	}
	if got := stdout.String(); got != "v1.2.3\n" {
		t.Fatalf("version --short = %q", got)
	}
}

func TestVersionJSONOutput(t *testing.T) {
	setTestVersion(t, "v1.2.3", "1234567890abcdef", "2026-11-26T12:00:00Z")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"version", "--json"}, &stdout, &stderr, nil); code != 0 {
		t.Fatalf("version --json exit=%d stderr=%s", code, stderr.String())
	}
	var info versionInfo
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		t.Fatalf("invalid version JSON: %v\n%s", err, stdout.String())
	}
	if info.Name != "goray" || info.Version != "v1.2.3" || info.Commit != "1234567890abcdef" || info.Built != "2026-11-26T12:00:00Z" {
		t.Fatalf("version JSON metadata = %+v", info)
	}
	if info.Go != runtime.Version() || info.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("version JSON runtime = %+v", info)
	}
}

func TestVersionTopLevelAliases(t *testing.T) {
	setTestVersion(t, "v1.2.3", "1234567", "2026-11-26T12:00:00Z")
	for _, arg := range []string{"-v", "-V", "--version"} {
		var stdout, stderr bytes.Buffer
		if code := runCLI([]string{arg}, &stdout, &stderr, nil); code != 0 {
			t.Fatalf("%s exit=%d stderr=%s", arg, code, stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), "goray v1.2.3\n") {
			t.Fatalf("%s output = %q", arg, stdout.String())
		}
	}
}

func TestVersionArgumentErrors(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"version", "extra"}, want: "version takes no arguments"},
		{args: []string{"version", "--short", "--json"}, want: "mutually exclusive"},
		{args: []string{"--version", "extra"}, want: "--version takes no arguments"},
	}
	for _, tc := range cases {
		var stdout, stderr bytes.Buffer
		if code := runCLI(tc.args, &stdout, &stderr, nil); code != 2 {
			t.Fatalf("%v exit=%d, want 2", tc.args, code)
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v stdout=%q stderr=%q, want %q", tc.args, stdout.String(), stderr.String(), tc.want)
		}
	}
}

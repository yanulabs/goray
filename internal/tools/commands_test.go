package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCommandHelperProcess(_ *testing.T) {
	if os.Getenv("GORAY_TEST_COMMAND_HELPER") != "1" {
		return
	}
	switch os.Getenv("GORAY_TEST_COMMAND_MODE") {
	case "exit":
		_, _ = fmt.Fprint(os.Stdout, os.Getenv("GORAY_TEST_COMMAND_OUTPUT"))
		os.Exit(3)
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "large":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("é", maxCommandOutput))
		os.Exit(0)
	default:
		_, _ = fmt.Fprint(os.Stdout, os.Getenv("GORAY_TEST_COMMAND_OUTPUT"))
		os.Exit(0)
	}
}

func runHelperCommand(t *testing.T, mode, output string) (string, bool) {
	t.Helper()
	t.Setenv("GORAY_TEST_COMMAND_HELPER", "1")
	t.Setenv("GORAY_TEST_COMMAND_MODE", mode)
	t.Setenv("GORAY_TEST_COMMAND_OUTPUT", output)
	return runCommand(context.Background(), t.TempDir(), os.Args[0], "-test.run=^TestCommandHelperProcess$")
}

func TestRunCommandSuccess(t *testing.T) {
	text, failed := runHelperCommand(t, "success", "hello")
	if failed || text != "OK:\nhello" {
		t.Fatalf("runCommand = %q, failed=%v", text, failed)
	}
}

func TestRunCommandExitFailure(t *testing.T) {
	text, failed := runHelperCommand(t, "exit", "boom")
	if !failed || !strings.Contains(text, "FAILED (exit status 3)") || !strings.Contains(text, "boom") {
		t.Fatalf("runCommand = %q, failed=%v", text, failed)
	}
}

func TestRunCommandTimeout(t *testing.T) {
	previous := commandTimeout
	commandTimeout = 100 * time.Millisecond
	t.Cleanup(func() { commandTimeout = previous })

	text, failed := runHelperCommand(t, "sleep", "")
	if !failed || !strings.Contains(text, "timeout after 100ms") {
		t.Fatalf("runCommand = %q, failed=%v", text, failed)
	}
}

func TestRunCommandTruncatesValidUTF8(t *testing.T) {
	text, failed := runHelperCommand(t, "large", "")
	if failed {
		t.Fatalf("runCommand failed: %s", text)
	}
	if !strings.Contains(text, "... [output truncated at 64 KiB]") {
		t.Fatal("missing truncation marker")
	}
	if !utf8.ValidString(text) {
		t.Fatal("truncated output is not valid UTF-8")
	}
	if len(text) > maxCommandOutput+64 {
		t.Fatalf("retained output = %d bytes, want at most %d plus marker", len(text), maxCommandOutput)
	}
}

func TestBoundedOutput(t *testing.T) {
	marker := "\n... [output truncated at 64 KiB]"
	cases := []struct {
		name   string
		limit  int
		writes [][]byte
		want   string
	}{
		{name: "below limit", limit: 8, writes: [][]byte{[]byte("hello")}, want: "hello"},
		{name: "exactly limit", limit: 4, writes: [][]byte{[]byte("éé")}, want: "éé"},
		{name: "split rune", limit: 3, writes: [][]byte{{'a', 'b', 0xC3, 0xA9, 'c'}}, want: "ab" + marker},
		{name: "across writes", limit: 8, writes: [][]byte{[]byte("12é"), []byte("34é"), []byte("x")}, want: "12é34é" + marker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := &boundedOutput{limit: tc.limit}
			for _, write := range tc.writes {
				n, err := out.Write(write)
				if err != nil || n != len(write) {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			got := string(out.Bytes())
			if got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("output is not valid UTF-8: %q", got)
			}
		})
	}
}

func TestBoundedOutputDiscardsLargeWrites(t *testing.T) {
	out := &boundedOutput{limit: maxCommandOutput}
	chunk := []byte(strings.Repeat("é", 32*1024))
	for i := 0; i < 64; i++ {
		if n, err := out.Write(chunk); err != nil || n != len(chunk) {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	got := out.Bytes()
	if !utf8.Valid(got) {
		t.Fatal("bounded output is not valid UTF-8")
	}
	if len(got) > maxCommandOutput+len("\n... [output truncated at 64 KiB]") {
		t.Fatalf("retained %d bytes for a %d-byte limit", len(got), maxCommandOutput)
	}
}

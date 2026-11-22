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

func TestCommandHelperProcess(t *testing.T) {
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
}

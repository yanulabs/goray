package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestServe(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":5,"method":"shutdown"}`,
	}, "\n")

	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input), &out, []Tool{{
		Name:        "echo",
		Description: "Echo text",
		InputSchema: map[string]any{"type": "object"},
		Run: func(_ context.Context, args map[string]any) (string, bool) {
			return "echo:" + args["text"].(string), false
		},
	}})

	srv.SetVersion("v9.9.9")
	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	responses := decodeResponses(t, out.String())
	if len(responses) != 5 {
		t.Fatalf("responses = %d, want 5\n%s", len(responses), out.String())
	}

	init := resultOf(t, responses["1"])
	if init["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocol = %v", init["protocolVersion"])
	}
	serverInfo := init["serverInfo"].(map[string]any)
	if serverInfo["version"] != "v9.9.9" || serverInfo["name"] != "goray" {
		t.Fatalf("serverInfo = %v", serverInfo)
	}

	list := resultOf(t, responses["2"])
	tools := list["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" {
		t.Fatalf("tools = %v", tools)
	}

	call := resultOf(t, responses["3"])
	content := call["content"].([]any)
	text := content[0].(map[string]any)["text"]
	if text != "echo:hi" || call["isError"] != false {
		t.Fatalf("tool result = %v", call)
	}

	if _, ok := responses["4"]; !ok {
		t.Fatal("missing ping response")
	}
}

func TestErrors(t *testing.T) {
	input := strings.Join([]string{
		`not json`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"missing"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"nope"}`,
	}, "\n")

	var out bytes.Buffer
	if err := NewServer(strings.NewReader(input), &out, nil).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	responses := decodeResponses(t, out.String())
	if len(responses) != 3 {
		t.Fatalf("responses = %d\n%s", len(responses), out.String())
	}
	if errorCode(t, responses["null"]) != -32700 {
		t.Fatal("missing parse error")
	}
	if resultOf(t, responses["1"])["isError"] != true {
		t.Fatal("unknown tool must be a tool error")
	}
	if errorCode(t, responses["2"]) != -32601 {
		t.Fatal("missing method-not-found error")
	}
}

func decodeResponses(t *testing.T, output string) map[string]map[string]json.RawMessage {
	t.Helper()
	out := map[string]map[string]json.RawMessage{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("bad response %q: %v", line, err)
		}
		var id string
		if value, ok := raw["id"]; ok {
			id = strings.Trim(string(value), `"`)
		}
		out[id] = raw
	}
	return out
}

func resultOf(t *testing.T, raw map[string]json.RawMessage) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(raw["result"], &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	return result
}

func errorCode(t *testing.T, raw map[string]json.RawMessage) int {
	t.Helper()
	var errObj struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(raw["error"], &errObj); err != nil {
		t.Fatalf("error: %v", err)
	}
	return errObj.Code
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestServeReturnsWriteError(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	err := NewServer(strings.NewReader(input), failingWriter{}, nil).Serve(context.Background())
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Serve error = %v, want io.ErrClosedPipe", err)
	}
}

func TestNotificationIgnored(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"tools/list"}` + "\n"
	var out bytes.Buffer
	if err := NewServer(strings.NewReader(input), &out, nil).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("notification produced output: %s", out.String())
	}
}

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
		Annotations: &ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false},
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
	annotations, ok := tools[0].(map[string]any)["annotations"].(map[string]any)
	if !ok {
		t.Fatalf("tools[0] annotations missing: %v", tools[0])
	}
	for _, key := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
		if _, ok := annotations[key]; !ok {
			t.Fatalf("annotations missing %s: %v", key, annotations)
		}
	}
	if annotations["readOnlyHint"] != true || annotations["openWorldHint"] != false {
		t.Fatalf("annotations = %v", annotations)
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

func TestCancelInFlightToolSuppressesReply(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		cancelID string
	}{
		{name: "numeric", id: "7", cancelID: "7"},
		{name: "string", id: `"req-7"`, cancelID: `"req-7"`},
		{name: "escaped string", id: `"req-\u0037"`, cancelID: `"req-7"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr, pw := io.Pipe()
			var out bytes.Buffer
			started := make(chan struct{})
			finished := make(chan struct{})
			srv := NewServer(pr, &out, []Tool{{
				Name: "slow",
				Run: func(ctx context.Context, _ map[string]any) (string, bool) {
					close(started)
					<-ctx.Done()
					close(finished)
					return "late result", false
				},
			}})
			serveErr := make(chan error, 1)
			go func() { serveErr <- srv.Serve(context.Background()) }()

			if _, err := fmt.Fprintf(pw, `{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"slow","arguments":{}}}`+"\n", tc.id); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("tool did not start")
			}
			if _, err := fmt.Fprintf(pw, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%s}}`+"\n", tc.cancelID); err != nil {
				t.Fatal(err)
			}
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("notifications/cancelled did not cancel the tool context")
			}
			if err := pw.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-serveErr:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("server did not stop after stdin closed")
			}
			if strings.TrimSpace(out.String()) != "" {
				t.Fatalf("canceled request emitted a response: %s", out.String())
			}
		})
	}
}

func TestQueuedToolCallHonorsCancellation(t *testing.T) {
	cases := []struct {
		name         string
		cancelServer bool
	}{
		{name: "request cancellation"},
		{name: "server cancellation", cancelServer: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ran atomic.Bool
			srv := NewServer(strings.NewReader(""), io.Discard, []Tool{{
				Name: "queued",
				Run: func(context.Context, map[string]any) (string, bool) {
					ran.Store(true)
					return "ran", false
				},
			}})
			for i := 0; i < maxConcurrentReads; i++ {
				srv.readSem <- struct{}{}
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var wg sync.WaitGroup
			srv.callTool(ctx, Request{
				ID:     json.RawMessage("42"),
				Method: "tools/call",
				Params: json.RawMessage(`{"name":"queued","arguments":{}}`),
			}, false, &wg)

			if tc.cancelServer {
				cancel()
			} else {
				srv.handle(context.Background(), Request{
					Method: "notifications/cancelled",
					Params: json.RawMessage(`{"requestId":42}`),
				}, &sync.WaitGroup{})
			}

			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("queued tool call did not exit after cancellation")
			}
			if ran.Load() {
				t.Fatal("queued tool ran after cancellation")
			}
		})
	}
}

func TestCanonicalRequestID(t *testing.T) {
	cases := map[string]string{
		`1`:          `1`,
		` 1 `:        `1`,
		`"abc"`:      `"abc"`,
		`"a\u0062c"`: `"abc"`,
		`null`:       "",
		``:           "",
	}
	for raw, want := range cases {
		if got := canonicalRequestID(json.RawMessage(raw)); got != want {
			t.Fatalf("canonicalRequestID(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSendFallsBackToInternalError(t *testing.T) {
	var out bytes.Buffer
	srv := NewServer(strings.NewReader(""), &out, nil)
	srv.send(Response{ID: json.RawMessage("9"), Result: make(chan int)})

	responses := decodeResponses(t, out.String())
	response, ok := responses["9"]
	if !ok {
		t.Fatalf("missing fallback response:\n%s", out.String())
	}
	if code := errorCode(t, response); code != -32603 {
		t.Fatalf("fallback error code = %d, want -32603", code)
	}
}

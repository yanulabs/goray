package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func TestListenLocalAndBanner(t *testing.T) {
	ln, err := listenLocal(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address = %T", ln.Addr())
	}
	url := localURL(ln)
	if want := fmt.Sprintf(":%d/", addr.Port); !strings.HasSuffix(url, want) {
		t.Fatalf("url = %q, want suffix %q", url, want)
	}
	var banner bytes.Buffer
	printServeBanner(&banner, url)
	for _, want := range []string{"goray dev", "Local:", url, "MCP:", "stdio"} {
		if !strings.Contains(banner.String(), want) {
			t.Fatalf("banner missing %q:\n%s", want, banner.String())
		}
	}
}

func occupyPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func TestListenConfiguredFallbackStrictAndErrors(t *testing.T) {
	busy := occupyPort(t)

	result, err := listenConfigured(listenConfig{port: busy, portRange: 200, host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = result.listener.Close() }()
	if !result.fallback || result.port == busy || result.requested != busy {
		t.Fatalf("result = %+v", result)
	}

	if _, err := listenConfigured(listenConfig{port: busy, portRange: 1, host: "127.0.0.1"}); err == nil ||
		!strings.Contains(err.Error(), "no free port in range") {
		t.Fatalf("exhausted range error = %v", err)
	}
	if _, err := listenConfigured(listenConfig{port: busy, portRange: 5, host: "127.0.0.1", strict: true}); err == nil ||
		!strings.Contains(err.Error(), "strict port") {
		t.Fatalf("strict error = %v", err)
	}

	zero, err := listenConfigured(listenConfig{port: 0, portRange: 10, host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zero.listener.Close() }()
	if zero.port == 0 || zero.fallback {
		t.Fatalf("port 0 result = %+v", zero)
	}

	if _, err := listenConfigured(listenConfig{port: 0, portRange: 10, host: "invalid host name"}); err == nil ||
		isAddrInUse(err) {
		t.Fatalf("invalid host error = %v", err)
	}
}

func TestServePortEnvAndFlagPrecedence(t *testing.T) {
	t.Setenv("GORAY_PORT", "5000")
	t.Setenv("GORAY_PORT_RANGE", "5")
	t.Setenv("GORAY_HOST", "0.0.0.0")
	t.Setenv("GORAY_STRICT_PORT", "true")
	t.Setenv("GORAY_PORT_FILE", "/tmp/env-port-file")
	opts, err := parseServeArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.port != 5000 || opts.portRange != 5 || opts.host != "0.0.0.0" || !opts.strictPort || opts.portFile != "/tmp/env-port-file" {
		t.Fatalf("env options = %+v", opts)
	}
	opts, err = parseServeArgs([]string{"--port", "5001", "--port-range", "7", "--host", "127.0.0.1", "--strict-port=false", "--port-file", "/tmp/flag-port-file"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.port != 5001 || opts.portRange != 7 || opts.host != "127.0.0.1" || opts.strictPort || opts.portFile != "/tmp/flag-port-file" {
		t.Fatalf("flag options = %+v", opts)
	}
	if _, err := parseServeArgs([]string{"--port-range", "0"}); err == nil {
		t.Fatal("zero port range should fail")
	}
	opts, err = parseServeArgs([]string{"--retain-packages"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.retainPackages {
		t.Fatal("--retain-packages was not parsed")
	}
	if !wantsHelp([]string{"--retain-packages", "--help"}, serveValueFlags) {
		t.Fatal("--help after boolean --retain-packages was not recognized")
	}
}

func TestServePortFileAndMovedPortNotice(t *testing.T) {
	root, _, _ := newTestModule(t)
	busy := occupyPort(t)
	portFile := filepath.Join(t.TempDir(), "goray.port")
	var stdout, stderr bytes.Buffer
	var opened string
	var portFileBody string
	opener := func(url string) error {
		opened = url
		data, err := os.ReadFile(portFile)
		if err != nil {
			return err
		}
		portFileBody = string(data)
		return nil
	}
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"shutdown"}` + "\n")
	opts := serveOptions{
		path: root, dbPath: ":memory:", logLevel: "off", port: busy, portRange: 200,
		host: "127.0.0.1", portFile: portFile, noWatch: true, open: true,
	}
	if err := serveContext(context.Background(), opts, input, &stdout, &stderr, opener); err != nil {
		t.Fatal(err)
	}
	if opened == "" || !strings.Contains(opened, ":") {
		t.Fatalf("opened URL = %q", opened)
	}
	port := listenerPortString(opened)
	if port == strconv.Itoa(busy) {
		t.Fatalf("serve did not move off busy port %d", busy)
	}
	if !strings.Contains(portFileBody, "GORAY_REPORT_URL="+opened) || !strings.Contains(portFileBody, "PID=") {
		t.Fatalf("port file = %q", portFileBody)
	}
	if _, err := os.Stat(portFile); !os.IsNotExist(err) {
		t.Fatalf("port file not removed on clean shutdown: %v", err)
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("Port %d is in use", busy)) {
		t.Fatalf("missing moved-port notice:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "GORAY_REPORT_URL="+opened) {
		t.Fatalf("missing machine-readable URL:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "GORAY_REPORT_URL") {
		t.Fatalf("URL leaked to stdout:\n%s", stdout.String())
	}
}

func TestConcurrentServeContextUsesDistinctPorts(t *testing.T) {
	root, _, _ := newTestModule(t)
	type outcome struct {
		url string
		err error
	}
	results := make(chan outcome, 2)
	run := func() {
		var stdout, stderr bytes.Buffer
		var url string
		opener := func(value string) error { url = value; return nil }
		input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"shutdown"}` + "\n")
		opts := serveOptions{
			path: root, dbPath: ":memory:", logLevel: "off", port: 0,
			portRange: 10, host: "127.0.0.1", noWatch: true, open: true,
		}
		err := serveContext(context.Background(), opts, input, &stdout, &stderr, opener)
		results <- outcome{url: url, err: err}
	}
	go run()
	go run()
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("serve errors: %v / %v", first.err, second.err)
	}
	if first.url == "" || second.url == "" || first.url == second.url {
		t.Fatalf("URLs = %q / %q", first.url, second.url)
	}
}

// TestServeContextWatcherShutdown is a regression test for the permanent
// shutdown deadlock: serveContext must cancel its context when Serve returns on
// its own, before the deferred WaitForAuto runs. With the old defer order this
// test times out because AutoRebuild only exits when ctx is canceled.
func TestServeContextWatcherShutdown(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{name: "mcp shutdown", input: `{"jsonrpc":"2.0","id":1,"method":"shutdown"}` + "\n"},
		{name: "stdin eof", input: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, _, _ := newTestModule(t)
			var stdout, stderr bytes.Buffer
			opts := serveOptions{
				path: root, dbPath: ":memory:", logLevel: "off",
				port: 0, noWatch: false,
			}
			done := make(chan error, 1)
			go func() {
				done <- serveContext(context.Background(), opts, strings.NewReader(tc.input), &stdout, &stderr, nil)
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("serveContext = %v\nstderr:\n%s", err, stderr.String())
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("serveContext did not exit after %s; stderr:\n%s", tc.name, stderr.String())
			}
		})
	}
}

func listenerPortString(url string) string {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(url, "http://"), "/")
	if i := strings.LastIndex(trimmed, ":"); i >= 0 {
		return trimmed[i+1:]
	}
	return ""
}

func TestReportSnapshotInvalidatesOnCacheGeneration(t *testing.T) {
	root, _, _ := newTestModule(t)
	summaries, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = summaries.Close() }()
	logger := slog.New(slog.DiscardHandler)
	store := index.NewStore(root, logger)
	store.SetBuildHook(func(idx *index.Index) error {
		return summaries.Sync(context.Background(), idx)
	})
	store.Start()
	defer store.WaitForBuild()

	snapshot := &reportSnapshot{}
	ctx := context.Background()
	if _, err := snapshot.get(ctx, store, summaries); err != nil {
		t.Fatal(err)
	}
	snapshot.mu.Lock()
	snapshot.data.Repo = "cached"
	snapshot.data.GeneratedAt = "old"
	snapshot.mu.Unlock()
	second, err := snapshot.get(ctx, store, summaries)
	if err != nil {
		t.Fatal(err)
	}
	if second.Repo != "cached" {
		t.Fatalf("unchanged snapshot rebuilt: repo = %q", second.Repo)
	}
	if second.GeneratedAt == "old" {
		t.Fatal("cached report kept a stale generated_at timestamp")
	}

	idx, err := store.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := summaries.Sync(ctx, idx); err != nil {
		t.Fatal(err)
	}
	third, err := snapshot.get(ctx, store, summaries)
	if err != nil {
		t.Fatal(err)
	}
	if third.Repo == "cached" {
		t.Fatal("cache generation change did not invalidate the report")
	}
}

func TestReportHandler(t *testing.T) {
	root, _, _ := newTestModule(t)
	summaries, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = summaries.Close() }()
	logger := slog.New(slog.DiscardHandler)
	store := index.NewStore(root, logger)
	store.SetBuildHook(func(idx *index.Index) error {
		return summaries.Sync(context.Background(), idx)
	})
	store.Start()
	defer store.WaitForBuild()

	handler := reportHandler(store, summaries, logger)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET / = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("content type = %q", got)
	}
	body := recorder.Body.String()
	for _, want := range []string{`id="goray-data"`, "goray · Architecture Report", `data-goray-live="true"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("report missing %q", want)
		}
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/report", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/report = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("API content type = %q", got)
	}
	var payload render.HTMLReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode API payload: %v", err)
	}
	if payload.ModulePath == "" || payload.GeneratedAt == "" {
		t.Fatalf("API payload missing metadata: %+v", payload)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/report", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/report = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok\n" {
		t.Fatalf("GET /healthz = %d %q", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / = %d", recorder.Code)
	}
}

func TestServeContextPrintsURLAndOpens(t *testing.T) {
	root, _, _ := newTestModule(t)
	var stdout, stderr bytes.Buffer
	var opened []string
	opener := func(url string) error {
		opened = append(opened, url)
		return nil
	}
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"shutdown"}` + "\n")
	opts := serveOptions{
		path: root, dbPath: ":memory:", logLevel: "off",
		port: 0, noWatch: true, open: true,
	}
	if err := serveContext(context.Background(), opts, input, &stdout, &stderr, opener); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 {
		t.Fatalf("opened = %v", opened)
	}
	url := opened[0]
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/") {
		t.Fatalf("opened URL = %q", url)
	}
	if !strings.Contains(stderr.String(), "➜  Local:   "+url) {
		t.Fatalf("stderr missing local URL:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "Local:") || strings.Contains(stdout.String(), "➜") {
		t.Fatalf("banner leaked to stdout:\n%s", stdout.String())
	}
}

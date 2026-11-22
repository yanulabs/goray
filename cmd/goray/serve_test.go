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
	"strings"
	"testing"

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

func TestReportHandler(t *testing.T) {
	root, _, _ := newTestModule(t)
	summaries, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = summaries.Close() }()
	logger := slog.New(slog.DiscardHandler)
	store := index.NewStore(root, logger)
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

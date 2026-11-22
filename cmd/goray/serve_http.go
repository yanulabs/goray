package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
	"github.com/yanulabs/goray/internal/report"
)

const (
	defaultServePort = 4321
	defaultPortRange = 100
	defaultServeHost = "127.0.0.1"
)

type listenConfig struct {
	port      int
	portRange int
	host      string
	strict    bool
}

type listenResult struct {
	listener  net.Listener
	requested int
	port      int
	fallback  bool
}

// listenConfigured reserves a listener atomically. Only EADDRINUSE advances to
// the next candidate; every other bind error is returned immediately.
func listenConfigured(cfg listenConfig) (listenResult, error) {
	if cfg.port < 0 || cfg.port > 65535 {
		return listenResult{}, fmt.Errorf("invalid port %d", cfg.port)
	}
	if cfg.host == "" {
		cfg.host = defaultServeHost
	}
	if cfg.portRange <= 0 {
		cfg.portRange = defaultPortRange
	}
	listen := func(p int) (net.Listener, error) {
		return net.Listen("tcp", net.JoinHostPort(cfg.host, strconv.Itoa(p)))
	}
	result := listenResult{requested: cfg.port}
	if cfg.port == 0 {
		ln, err := listen(0)
		if err != nil {
			return listenResult{}, fmt.Errorf("listen on %s:0: %w", cfg.host, err)
		}
		result.listener = ln
		result.port = listenerPort(ln)
		return result, nil
	}

	last := cfg.port + cfg.portRange - 1
	if last > 65535 {
		last = 65535
	}
	for p := cfg.port; p <= last; p++ {
		ln, err := listen(p)
		if err == nil {
			result.listener = ln
			result.port = p
			result.fallback = p != cfg.port
			return result, nil
		}
		if !isAddrInUse(err) {
			return listenResult{}, fmt.Errorf("listen on %s:%d: %w", cfg.host, p, err)
		}
		if cfg.strict {
			return listenResult{}, fmt.Errorf("port %d is already in use (strict port mode): %w", p, err)
		}
	}
	return listenResult{}, fmt.Errorf(
		"no free port in range %d-%d on %s; stop the process using one of these ports or pass --port 0",
		cfg.port, last, cfg.host,
	)
}

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

func listenerPort(ln net.Listener) int {
	if addr, ok := ln.Addr().(*net.TCPAddr); ok {
		return addr.Port
	}
	return 0
}

// listenLocal keeps the legacy behavior used by tests: try the requested port
// and the following 20 candidates, with no ephemeral fallback.
func listenLocal(port int) (net.Listener, error) {
	result, err := listenConfigured(listenConfig{port: port, portRange: 21, host: defaultServeHost})
	if err != nil {
		return nil, err
	}
	return result.listener, nil
}

func localURL(ln net.Listener) string {
	addr := ln.Addr().String()
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok && tcp.Port == 0 {
		addr = tcp.IP.String() + ":0"
	}
	return "http://" + addr + "/"
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func printServeBanner(w io.Writer, url string) {
	_, _ = fmt.Fprintf(w, "\n  goray dev\n\n  ➜  Local:   %s\n  ➜  MCP:     stdio\n  ➜  Press Ctrl+C to stop\n\n", url)
}

func startReportServer(ctx context.Context, ln net.Listener, store *index.Store, summaries *cache.Cache, logger *slog.Logger) *http.Server {
	server := &http.Server{
		Handler:           reportHandler(store, summaries, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("report server stopped", "err", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return server
}

func reportHandler(store *index.Store, summaries *cache.Cache, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	build := func(ctx context.Context) (render.HTMLReport, error) {
		idx, err := store.Wait(ctx)
		if err != nil {
			return render.HTMLReport{}, fmt.Errorf("index is not ready: %w", err)
		}
		return report.Build(ctx, idx, summaries)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data, err := build(r.Context())
		if err != nil {
			logger.Warn("report request failed", "err", err)
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		html, err := render.RenderHTMLLive(data)
		if err != nil {
			logger.Warn("report render failed", "err", err)
			http.Error(w, "report render failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.WriteString(w, html)
	})
	mux.HandleFunc("/api/report", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/report" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data, err := build(r.Context())
		if err != nil {
			logger.Warn("report API request failed", "err", err)
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := json.NewEncoder(w).Encode(data); err != nil {
			logger.Warn("report API encode failed", "err", err)
		}
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

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
	"time"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
	"github.com/yanulabs/goray/internal/report"
)

const defaultServePort = 4321

func listenLocal(port int) (net.Listener, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d", port)
	}
	listen := func(p int) (net.Listener, error) {
		return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
	}
	if port == 0 {
		return listen(0)
	}
	var lastErr error
	for p := port; p <= port+20 && p <= 65535; p++ {
		ln, err := listen(p)
		if err == nil {
			return ln, nil
		}
		lastErr = err
	}
	// Keep the MCP server usable even if every preferred port is taken.
	if ln, err := listen(0); err == nil {
		return ln, nil
	}
	return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w", port, lastErr)
}

func localURL(ln net.Listener) string {
	if addr, ok := ln.Addr().(*net.TCPAddr); ok {
		return fmt.Sprintf("http://127.0.0.1:%d/", addr.Port)
	}
	return "http://127.0.0.1/"
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

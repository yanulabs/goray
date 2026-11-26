package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
)

const maxConcurrentReads = 8

// Server is a JSON-RPC MCP server over a newline-delimited stdio stream.
type Server struct {
	in        io.Reader
	out       io.Writer
	byName    map[string]Tool
	specs     []ToolSpec
	version   string
	readSem   chan struct{}
	writeMu   sync.Mutex
	outMu     sync.Mutex
	writeErr  error
	writeErrC chan error

	// callsMu guards calls, which tracks the cancel function of every
	// in-flight tool call so notifications/cancelled can stop it.
	callsMu sync.Mutex
	calls   map[string]context.CancelFunc
}

// NewServer creates a server with the given transport and tool registry.
func NewServer(in io.Reader, out io.Writer, tools []Tool) *Server {
	s := &Server{
		in:        in,
		out:       out,
		byName:    make(map[string]Tool, len(tools)),
		specs:     make([]ToolSpec, 0, len(tools)),
		version:   "dev",
		readSem:   make(chan struct{}, maxConcurrentReads),
		writeErrC: make(chan error, 1),
		calls:     make(map[string]context.CancelFunc),
	}
	for _, tool := range tools {
		s.byName[tool.Name] = tool
		s.specs = append(s.specs, tool.spec())
	}
	return s
}

// SetVersion configures the version advertised during initialize.
func (s *Server) SetVersion(version string) {
	if version != "" {
		s.version = version
	}
}

// Serve reads JSON-RPC messages until stdin closes, shutdown is received, or
// ctx is done.
func (s *Server) Serve(ctx context.Context) (err error) {
	lines := make(chan []byte)
	errCh := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(s.in)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			select {
			case lines <- bytes.Clone(line):
			case <-ctx.Done():
				return
			}
		}
		errCh <- scanner.Err()
	}()

	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		if err == nil {
			err = s.pendingWriteError()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-s.writeErrC:
			return err
		case line, ok := <-lines:
			if !ok {
				if err := s.pendingWriteError(); err != nil {
					return err
				}
				return <-errCh
			}
			var req Request
			if err := json.Unmarshal(line, &req); err != nil {
				s.send(Response{ID: json.RawMessage("null"), Error: &Error{Code: -32700, Message: "invalid JSON"}})
				continue
			}
			if s.handle(ctx, req, &wg) {
				return nil
			}
		}
	}
}

func (s *Server) handle(ctx context.Context, req Request, wg *sync.WaitGroup) (shutdown bool) {
	notification := isNotification(req)

	switch req.Method {
	case "initialize":
		protocol := "2024-11-05"
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &params) == nil && params.ProtocolVersion != "" {
			protocol = params.ProtocolVersion
		}
		s.reply(notification, req.ID, map[string]any{
			"protocolVersion": protocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "goray", "version": s.version},
		})

	case "notifications/initialized", "initialized":
		// No response.

	case "notifications/cancelled":
		s.cancelCall(req.Params)

	case "ping":
		s.reply(notification, req.ID, map[string]any{})

	case "shutdown":
		s.reply(notification, req.ID, map[string]any{})
		return true

	case "tools/list":
		s.reply(notification, req.ID, map[string]any{"tools": s.specs})

	case "tools/call":
		s.callTool(ctx, req, notification, wg)

	default:
		if !notification {
			s.send(Response{ID: req.ID, Error: &Error{Code: -32601, Message: "method not found: " + req.Method}})
		}
	}
	return false
}

func (s *Server) callTool(ctx context.Context, req Request, notification bool, wg *sync.WaitGroup) {
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &call); err != nil {
		if !notification {
			s.send(Response{ID: req.ID, Error: &Error{Code: -32602, Message: "invalid tools/call params"}})
		}
		return
	}
	if call.Arguments == nil {
		call.Arguments = map[string]any{}
	}

	tool, ok := s.byName[call.Name]
	if !ok {
		s.reply(notification, req.ID, toolResult("unknown tool: "+call.Name, true))
		return
	}

	reqCtx, cancel := context.WithCancel(ctx)
	key := canonicalRequestID(req.ID)
	tracked := !notification && key != ""
	if tracked {
		s.callsMu.Lock()
		s.calls[key] = cancel
		s.callsMu.Unlock()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		defer func() {
			if tracked {
				s.callsMu.Lock()
				delete(s.calls, key)
				s.callsMu.Unlock()
			}
		}()

		if tool.Exclusive {
			s.writeMu.Lock()
			defer s.writeMu.Unlock()
			if reqCtx.Err() != nil {
				return
			}
		} else {
			select {
			case s.readSem <- struct{}{}:
				defer func() { <-s.readSem }()
			case <-reqCtx.Done():
				return
			}
			if reqCtx.Err() != nil {
				return
			}
		}

		text, failed := tool.Run(reqCtx, call.Arguments)
		if notification || !s.finishCall(tracked, key, reqCtx) {
			return
		}
		s.reply(false, req.ID, toolResult(text, failed))
	}()
}

// cancelCall cancels one in-flight request by its canonical JSON-RPC id. The
// cancel function runs while callsMu is held so a completion racing with a
// cancellation either observes the canceled context or removes the entry
// before the cancellation can arrive; in both cases a canceled request never
// emits a reply.
func (s *Server) cancelCall(params json.RawMessage) {
	var cancelled struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &cancelled) != nil {
		return
	}
	key := canonicalRequestID(cancelled.RequestID)
	if key == "" {
		return
	}
	s.callsMu.Lock()
	if cancel := s.calls[key]; cancel != nil {
		cancel()
	}
	s.callsMu.Unlock()
}

// finishCall stops tracking a request and reports whether it may reply. A
// request canceled before this point is suppressed.
func (s *Server) finishCall(tracked bool, key string, reqCtx context.Context) bool {
	if !tracked {
		return reqCtx.Err() == nil
	}
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	delete(s.calls, key)
	return reqCtx.Err() == nil
}

// canonicalRequestID normalizes a JSON-RPC id so a cancellation notification
// matches regardless of insignificant whitespace or string escaping.
func canonicalRequestID(id json.RawMessage) string {
	trimmed := bytes.TrimSpace(id)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return string(trimmed)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return string(trimmed)
	}
	return string(canonical)
}

func (s *Server) reply(notification bool, id json.RawMessage, result any) {
	if notification {
		return
	}
	s.send(Response{ID: id, Result: result})
}

func (s *Server) pendingWriteError() error {
	select {
	case err := <-s.writeErrC:
		return err
	default:
		return nil
	}
}

func (s *Server) send(resp Response) {
	resp.JSONRPC = "2.0"
	data, err := json.Marshal(resp)
	if err != nil {
		// Never leave the client waiting on an id whose result could not be
		// encoded. A minimal error response usually marshals even when the
		// result contains an unsupported value.
		fallback := Response{
			JSONRPC: "2.0",
			ID:      resp.ID,
			Error:   &Error{Code: -32603, Message: "internal error: failed to encode response"},
		}
		data, err = json.Marshal(fallback)
		if err != nil {
			fallback.ID = json.RawMessage("null")
			data, err = json.Marshal(fallback)
			if err != nil {
				return
			}
		}
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	if s.writeErr != nil {
		return
	}
	if _, err := s.out.Write(append(data, '\n')); err != nil {
		s.writeErr = err
		select {
		case s.writeErrC <- err:
		default:
		}
	}
}

func toolResult(text string, failed bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": failed,
	}
}

func isNotification(req Request) bool {
	trimmed := bytes.TrimSpace(req.ID)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

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

type Server struct {
	in      io.Reader
	out     io.Writer
	byName  map[string]Tool
	specs   []ToolSpec
	readSem chan struct{}
	writeMu sync.Mutex
	outMu   sync.Mutex
}

func NewServer(in io.Reader, out io.Writer, tools []Tool) *Server {
	s := &Server{
		in:      in,
		out:     out,
		byName:  make(map[string]Tool, len(tools)),
		specs:   make([]ToolSpec, 0, len(tools)),
		readSem: make(chan struct{}, maxConcurrentReads),
	}
	for _, tool := range tools {
		s.byName[tool.Name] = tool
		s.specs = append(s.specs, tool.spec())
	}
	return s
}

// Serve reads JSON-RPC messages until stdin closes, shutdown is received, or
// ctx is done.
func (s *Server) Serve(ctx context.Context) error {
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
	defer wg.Wait()

	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-lines:
			if !ok {
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
			"serverInfo":      map[string]any{"name": "go-mcp-server", "version": "0.1.0"},
		})

	case "notifications/initialized", "initialized", "notifications/cancelled":
		// No response.

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

	wg.Add(1)
	go func() {
		defer wg.Done()
		if tool.Exclusive {
			s.writeMu.Lock()
			defer s.writeMu.Unlock()
		} else {
			s.readSem <- struct{}{}
			defer func() { <-s.readSem }()
		}
		text, failed := tool.Run(ctx, call.Arguments)
		s.reply(notification, req.ID, toolResult(text, failed))
	}()
}

func (s *Server) reply(notification bool, id json.RawMessage, result any) {
	if notification {
		return
	}
	s.send(Response{ID: id, Result: result})
}

func (s *Server) send(resp Response) {
	resp.JSONRPC = "2.0"
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_, _ = s.out.Write(append(data, '\n'))
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

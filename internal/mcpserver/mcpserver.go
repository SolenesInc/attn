package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
)

const latestProtocolVersion = "2025-11-25"

var protocolVersions = []string{latestProtocolVersion, "2025-06-18", "2025-03-26", "2024-11-05"}

type Tool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  *Annotations    `json:"annotations,omitempty"`
	Meta         map[string]any  `json:"_meta,omitempty"`
}

type Annotations struct {
	Title        string `json:"title,omitempty"`
	ReadOnlyHint bool   `json:"readOnlyHint,omitempty"`
}

type Result struct {
	Content           []Content `json:"content"`
	StructuredContent any       `json:"structuredContent,omitempty"`
	IsError           bool      `json:"isError,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func Text(text string) Content { return Content{Type: "text", Text: text} }

type Call func(ctx context.Context, tool string, arguments, meta json.RawMessage) Result

type Server struct {
	name         string
	version      string
	instructions string
	tools        []Tool
}

func New(name, version, instructions string, tools []Tool) *Server {
	sorted := slices.Clone(tools)
	slices.SortFunc(sorted, func(a, b Tool) int { return strings.Compare(a.Name, b.Name) })
	return &Server{name: name, version: version, instructions: instructions, tools: sorted}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

func (s *Server) Serve(w http.ResponseWriter, r *http.Request, call Call) (method string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return ""
	}
	if media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); media != "application/json" {
		http.Error(w, "Content-Type must be 'application/json'", http.StatusUnsupportedMediaType)
		return ""
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading the request: %v", err), http.StatusBadRequest)
		return ""
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("a request is one JSON-RPC message: %v", err), http.StatusBadRequest)
		return ""
	}
	if len(req.ID) == 0 || req.Method == "" {
		w.WriteHeader(http.StatusAccepted)
		return req.Method
	}
	reply := response{JSONRPC: "2.0", ID: req.ID}
	reply.Result, reply.Error = s.answer(r.Context(), req, call)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
	return req.Method
}

func (s *Server) answer(ctx context.Context, req request, call Call) (any, *rpcError) {
	if req.JSONRPC != "2.0" {
		return nil, &rpcError{Code: codeInvalidRequest, Message: `jsonrpc must be "2.0"`}
	}
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		version := latestProtocolVersion
		if slices.Contains(protocolVersions, params.ProtocolVersion) {
			version = params.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": s.name, "version": s.version},
			"instructions":    s.instructions,
		}, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      json.RawMessage `json:"_meta"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(s.tools, func(t Tool) bool { return t.Name == params.Name }) {
			return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool %q", params.Name)}
		}
		return call(ctx, params.Name, params.Arguments, params.Meta), nil
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("method %q is not served; this server answers initialize, ping, tools/list and tools/call", req.Method)}
	}
}

func decodeParams(raw json.RawMessage, into any) *rpcError {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("params: %v", err)}
	}
	return nil
}

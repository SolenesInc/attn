package fakeagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const methodCallMCPTool = "call_mcp_tool"

type ToolResult struct {
	IsError    bool            `json:"isError"`
	Text       string          `json:"text"`
	Structured json.RawMessage `json:"structured"`
}

type mcpToolCall struct {
	ThreadID  string         `json:"threadId"`
	Server    string         `json:"server"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// CallTool has the agent of conversation call tool on the MCP server its conversation's config names server.
func (s *CodexServer) CallTool(conversation, server, tool string, arguments map[string]any) ToolResult {
	s.t.Helper()
	var result ToolResult
	s.call(methodCallMCPTool, mcpToolCall{ThreadID: conversation, Server: server, Tool: tool, Arguments: arguments}, &result)
	return result
}

type codexHTTPServer struct {
	url           string
	authorization string
}

func codexHTTPServerIn(config map[string]any, name string) (codexHTTPServer, error) {
	prefix := "mcp_servers." + name + "."
	text := func(field string) string {
		value, _ := config[prefix+field].(string)
		return value
	}
	server := codexHTTPServer{url: text("url"), authorization: text("http_headers.Authorization")}
	if server.url == "" {
		return server, fmt.Errorf("no MCP server %s with a url in this conversation's config", name)
	}
	return server, nil
}

func callCodexTool(config map[string]any, call mcpToolCall) (ToolResult, error) {
	server, err := codexHTTPServerIn(config, call.Server)
	if err != nil {
		return ToolResult{}, err
	}
	if _, err := server.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "codex-mcp-client", "version": "fake"}}); err != nil {
		return ToolResult{}, err
	}
	raw, err := server.request("tools/call", map[string]any{"name": call.Tool, "arguments": call.Arguments, "_meta": call.Meta})
	if err != nil {
		return ToolResult{}, err
	}
	var answer struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return ToolResult{}, err
	}
	result := ToolResult{IsError: answer.IsError, Structured: answer.Structured}
	for _, content := range answer.Content {
		result.Text += content.Text
	}
	return result, nil
}

func (s codexHTTPServer) request(method string, params any) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if s.authorization != "" {
		req.Header.Set("Authorization", s.authorization)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	reply, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: %s: %s", method, s.url, resp.Status, strings.TrimSpace(string(reply)))
	}
	var answer struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(reply, &answer); err != nil {
		return nil, fmt.Errorf("%s %s answered %q: %w", method, s.url, reply, err)
	}
	if answer.Error != nil {
		return nil, fmt.Errorf("%s %s: %s", method, s.url, answer.Error.Message)
	}
	return answer.Result, nil
}

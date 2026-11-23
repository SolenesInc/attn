package fakeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

const (
	methodMCPRegistered = "mcp_registered"
	methodCallTool      = "call_tool"
	methodFork          = "fork"
	methodForks         = "forks"
	methodAwaitMCP      = "await_mcp"
	methodRefuseMCP     = "refuse_mcp"
	methodMCPHeader     = "mcp_header"
)

// ServerMCPServer is one MCP server a conversation uses, by the name a client registered it with.
type ServerMCPServer struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type mcpRegistration struct {
	URL     string
	Headers map[string]string
	conn    *appServerConn
	request uint64
}

// ServerMCPRegistration is what the server holds for a registered MCP server; it names the headers, not their values.
type ServerMCPRegistration struct {
	URL     string   `json:"url"`
	Headers []string `json:"headers"`
}

// ServerToolResult is an MCP tools/call result as the server received it.
type ServerToolResult struct {
	IsError           bool                    `json:"isError"`
	Content           []struct{ Text string } `json:"content"`
	StructuredContent json.RawMessage         `json:"structuredContent"`
	Refused           string                  `json:"refused,omitempty"`
}

func (r ServerToolResult) Text() string {
	var out string
	for _, c := range r.Content {
		out += c.Text
	}
	return out
}

func (s *appServer) register(c *appServerConn, id uint64, raw json.RawMessage) (any, string) {
	var p struct {
		Name    string            `json:"name"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Name == "" || p.URL == "" {
		return nil, "InvalidMcpServer"
	}
	s.mu.Lock()
	if s.refuseMCP != "" {
		defer s.mu.Unlock()
		return nil, s.refuseMCP
	}
	s.mcp[p.Name] = mcpRegistration{URL: p.URL, Headers: p.Headers, conn: c, request: id}
	s.mcpChangedLocked()
	s.mu.Unlock()
	c.send(map[string]any{"jsonrpc": "2.0", "chunk": true, "id": id, "result": []any{map[string]string{"name": p.Name}}})
	return nil, replyLost
}

func (s *appServer) interruptRegistration(c *appServerConn, raw json.RawMessage) {
	var p struct {
		RequestID uint64 `json:"requestId"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, r := range s.mcp {
		if r.conn == c && r.request == p.RequestID {
			delete(s.mcp, name)
			s.mcpChangedLocked()
		}
	}
}

func (s *appServer) mcpChangedLocked() {
	if s.mcpChanged != nil {
		close(s.mcpChanged)
	}
	s.mcpChanged = make(chan struct{})
}

type awaitMCPParams struct {
	Name       string `json:"name"`
	Registered bool   `json:"registered"`
}

func (s *appServer) awaitMCP(raw json.RawMessage) (any, error) {
	var p awaitMCPParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	deadline := time.After(HangGuard)
	for {
		s.mu.Lock()
		_, registered := s.mcp[p.Name]
		if s.mcpChanged == nil {
			s.mcpChanged = make(chan struct{})
		}
		changed := s.mcpChanged
		s.mu.Unlock()
		if registered == p.Registered {
			return struct{}{}, nil
		}
		select {
		case <-changed:
		case <-deadline:
			return nil, fmt.Errorf("MCP server %s registered %v, still not %v after %s", p.Name, registered, p.Registered, HangGuard)
		}
	}
}

// AwaitMCPRegistration waits until a client has registered the MCP server name, or until none has.
func (s *AppServer) AwaitMCPRegistration(name string, registered bool) {
	s.t.Helper()
	s.call(methodAwaitMCP, awaitMCPParams{Name: name, Registered: registered}, nil)
}

func (s *appServer) registered() map[string]ServerMCPRegistration {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]ServerMCPRegistration{}
	for name, r := range s.mcp {
		headers := make([]string, 0, len(r.Headers))
		for header := range r.Headers {
			headers = append(headers, header)
		}
		sort.Strings(headers)
		out[name] = ServerMCPRegistration{URL: r.URL, Headers: headers}
	}
	return out
}

type callToolParams struct {
	Conversation string         `json:"conversation"`
	Tool         string         `json:"tool"`
	Arguments    map[string]any `json:"arguments"`
}

func (s *appServer) callTool(raw json.RawMessage) (any, error) {
	var p callToolParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	conv := s.convs[p.Conversation]
	var server *mcpRegistration
	if conv != nil {
		for _, used := range conv.MCPServers {
			if r, ok := s.mcp[used.Name]; ok {
				server = &r
				break
			}
		}
	}
	s.mu.Unlock()
	if conv == nil {
		return nil, fmt.Errorf("no conversation %s", p.Conversation)
	}
	if server == nil {
		return ServerToolResult{Refused: fmt.Sprintf("conversation %s uses no registered MCP server (it lists %v)", p.Conversation, conv.MCPServers)}, nil
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": p.Tool, "arguments": p.Arguments}})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for name, value := range server.Headers {
		req.Header.Set(name, value)
	}
	callerRoot := p.Conversation
	if conv.Lineage != nil && conv.Lineage.Root != "" {
		callerRoot = conv.Lineage.Root
	}
	req.Header.Set(s.flavor.callerHeader, callerRoot)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var reply struct {
		Result ServerToolResult `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if resp.StatusCode != http.StatusOK {
		var text bytes.Buffer
		_, _ = text.ReadFrom(resp.Body)
		return ServerToolResult{Refused: fmt.Sprintf("%s: %s", resp.Status, bytes.TrimSpace(text.Bytes()))}, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, err
	}
	if reply.Error != nil {
		return ServerToolResult{Refused: reply.Error.Message}, nil
	}
	return reply.Result, nil
}

// MCPRegistered lists the MCP servers clients have registered with the server now.
func (s *AppServer) MCPRegistered() map[string]ServerMCPRegistration {
	s.t.Helper()
	var out map[string]ServerMCPRegistration
	s.call(methodMCPRegistered, nil, &out)
	return out
}

// CallTool has conversation call tool on the first registered MCP server the conversation uses, as its agent would.
func (s *AppServer) CallTool(conversation, tool string, arguments map[string]any) ServerToolResult {
	s.t.Helper()
	var out ServerToolResult
	s.call(methodCallTool, callToolParams{Conversation: conversation, Tool: tool, Arguments: arguments}, &out)
	return out
}

type mcpHeaderParams struct {
	Server string `json:"server"`
	Header string `json:"header"`
}

func (s *appServer) mcpHeader(raw json.RawMessage) (any, error) {
	var p mcpHeaderParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mcp[p.Server].Headers[p.Header], nil
}

// MCPHeader is the value of header that a client registered the MCP server named server with, or "" when none.
func (s *AppServer) MCPHeader(server, header string) string {
	s.t.Helper()
	var out string
	s.call(methodMCPHeader, mcpHeaderParams{Server: server, Header: header}, &out)
	return out
}

type forkParams struct {
	Conversation string `json:"conversation"`
	ID           string `json:"id"`
	Author       string `json:"author"`
}

func (s *appServer) fork(raw json.RawMessage) (any, error) {
	var p forkParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.transcriptLocked(p.Conversation).Entries
	at := int64(0)
	if len(entries) > 0 {
		at = entries[len(entries)-1].ID
	}
	author := ServerAuthor{ID: "you", Name: "you"}
	if p.Author != "" {
		author.ID = p.Author
	}
	if _, refused := s.forkLocked(p.Conversation, p.ID, at, author); refused != "" {
		return nil, errors.New(refused)
	}
	return struct{}{}, nil
}

// forkLocked starts a conversation that inherits from's entries through at and goes on with a sigma.forked entry
// naming author, as Sigma's conversation/fork does. A fork with no id is named by its number.
func (s *appServer) forkLocked(from, id string, at int64, author ServerAuthor) (string, string) {
	parent := s.convs[from]
	if parent == nil {
		return "", "NoSuchConversation"
	}
	if id == "" {
		id = strconv.FormatInt(s.nextEntry+1, 10)
	}
	if s.convs[id] != nil {
		return "", "ConversationExists"
	}
	var inherited []serverEntry
	for _, entry := range s.transcriptLocked(from).Entries {
		if entry.ID <= at {
			inherited = append(inherited, entry)
		}
	}
	s.convs[id] = &ServerConversation{ID: id, Cwd: parent.Cwd, Target: parent.Target, Instructions: parent.Instructions, MCPServers: parent.MCPServers, Fork: &ServerFork{Of: from, At: at}, Status: "idle", requests: map[string]int{}}
	s.transcriptLocked(id).Entries = inherited
	s.appendLocked(id, serverEntry{Kind: "sigma.forked", Data: map[string]any{"author": author}})
	return id, ""
}

// RefuseMCPRegistration makes the server refuse every later mcp/register with code, as a server that cannot reach the MCP server does.
func (s *AppServer) RefuseMCPRegistration(code string) {
	s.t.Helper()
	s.call(methodRefuseMCP, map[string]string{"code": code}, nil)
}

type forksParams struct {
	Of string `json:"of"`
}

func (s *appServer) forks(raw json.RawMessage) (any, error) {
	var p forksParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	forks := []string{}
	for id, conv := range s.convs {
		if conv.Fork != nil && conv.Fork.Of == p.Of {
			forks = append(forks, id)
		}
	}
	sort.Strings(forks)
	return forks, nil
}

// Forks are the conversations forked from conversation of.
func (s *AppServer) Forks(of string) []string {
	s.t.Helper()
	var out []string
	s.call(methodForks, forksParams{Of: of}, &out)
	return out
}

// Fork starts conversation id on the server as a fork of conversation, as an agent's /fork does: it keeps the MCP servers it uses, and no attn session holds it yet.
func (s *AppServer) Fork(conversation, id string) {
	s.t.Helper()
	s.call(methodFork, forkParams{Conversation: conversation, ID: id}, nil)
}

// ForkAs forks conversation as id under author, as a client writing as author does when it forks and never opens the fork.
func (s *AppServer) ForkAs(conversation, id, author string) {
	s.t.Helper()
	s.call(methodFork, forkParams{Conversation: conversation, ID: id, Author: author}, nil)
}

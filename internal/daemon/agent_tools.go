package daemon

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

//go:embed agent_tools_generated.json
var agentToolDefinitions []byte

type agentToolDefinition struct {
	Command      string          `json:"command"`
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Core         bool            `json:"core"`
	ReadOnly     bool            `json:"read_only"`
	Stdin        []string        `json:"stdin"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema"`
}

const (
	agentToolsAll  = ""
	agentToolsCore = "core"
	agentToolsRest = "rest"
)

type agentCall struct {
	caller command.Caller
	who    string
	after  func()
}

type agentToolServers struct {
	once    sync.Once
	servers map[string]*mcp.Server
}

func (d *Daemon) agentToolsHandler() http.Handler {
	sdk := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return d.agentToolServers()[r.URL.Query().Get("tools")]
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.agentToolServers()[r.URL.Query().Get("tools")]; !ok {
			http.Error(w, fmt.Sprintf("tools=%s names no tool set; use tools=core, tools=rest, or leave it out for every tool", r.URL.Query().Get("tools")), http.StatusBadRequest)
			return
		}
		call := &agentCall{}
		verify := func(_ context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
			caller, who, err := d.agentCaller(token, r.Header)
			if err != nil {
				d.logf("agent tools: %s %s refused: %v", r.Method, r.URL.Path, err)
				return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
			}
			call.caller, call.who = caller, who
			return &auth.TokenInfo{Extra: map[string]any{"call": call}}, nil
		}
		auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(sdk).ServeHTTP(w, r)
		if call.after != nil {
			_ = http.NewResponseController(w).Flush()
			call.after()
		}
	})
}

func (d *Daemon) agentCaller(token string, header http.Header) (command.Caller, string, error) {
	credential, err := d.agentKey.verify(token)
	if err != nil {
		return command.Caller{}, "", err
	}
	switch credential.kind {
	case credentialTerminal:
		terminal := protocol.TerminalID(credential.name)
		_, _, linked := d.linkCaller(terminal)
		if !linked && !d.terminalLive(terminal) {
			return command.Caller{}, "", fmt.Errorf("terminal %s has ended", terminal)
		}
		who := "terminal " + string(terminal)
		session := d.sessionInTerminal(terminal)
		if session == "" {
			return command.Caller{Terminal: terminal}, who, nil
		}
		return d.sessionCaller(session), who, nil
	default:
		l := d.serverLinkFor(credential.name)
		if l == nil || l.callerConversationHeader() == "" {
			return command.Caller{}, "", fmt.Errorf("harness %s has no server that names its conversations", credential.name)
		}
		name := l.callerConversationHeader()
		conversation := strings.TrimSpace(header.Get(name))
		if conversation == "" {
			return command.Caller{}, "", fmt.Errorf("the %s server's request has no %s header", credential.name, name)
		}
		session := l.holder(conversation)
		if session == "" {
			return command.Caller{}, "", fmt.Errorf("no session of %s holds conversation %s", credential.name, conversation)
		}
		return d.sessionCaller(session), credential.name + " conversation " + conversation, nil
	}
}

func (d *Daemon) sessionCaller(session protocol.SessionID) command.Caller {
	c := command.Caller{Session: session}
	if row := d.store.Get(session); row != nil {
		c.Profile = row.ProfileID
	}
	return c
}

func (d *Daemon) agentToolServers() map[string]*mcp.Server {
	d.agentToolSet.once.Do(func() {
		var catalog struct {
			Name         string                `json:"name"`
			Instructions string                `json:"instructions"`
			Tools        []agentToolDefinition `json:"tools"`
		}
		if err := json.Unmarshal(agentToolDefinitions, &catalog); err != nil {
			panic(err)
		}
		servers := map[string]*mcp.Server{}
		for _, set := range []string{agentToolsAll, agentToolsCore, agentToolsRest} {
			servers[set] = mcp.NewServer(&mcp.Implementation{Name: catalog.Name, Version: buildinfo.Version}, &mcp.ServerOptions{Instructions: catalog.Instructions})
		}
		for _, tool := range catalog.Tools {
			add := d.agentTool(tool)
			add(servers[agentToolsAll])
			if tool.Core {
				add(servers[agentToolsCore])
			} else {
				add(servers[agentToolsRest])
			}
		}
		d.agentToolSet.servers = servers
	})
	return d.agentToolSet.servers
}

func (d *Daemon) agentTool(def agentToolDefinition) func(*mcp.Server) {
	var input jsonschema.Schema
	if err := json.Unmarshal(def.InputSchema, &input); err != nil {
		panic(fmt.Sprintf("%s: input schema: %v", def.Name, err))
	}
	resolved, err := input.Resolve(nil)
	if err != nil {
		panic(fmt.Sprintf("%s: input schema: %v", def.Name, err))
	}
	tool := &mcp.Tool{
		Name:        def.Name,
		Title:       def.Title,
		Description: def.Description,
		InputSchema: def.InputSchema,
		Annotations: &mcp.ToolAnnotations{Title: def.Title, ReadOnlyHint: def.ReadOnly},
	}
	if len(def.OutputSchema) > 0 {
		tool.OutputSchema = def.OutputSchema
	}
	if def.Core {
		tool.Meta = mcp.Meta{"anthropic/alwaysLoad": true}
	}
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		call := req.Extra.TokenInfo.Extra["call"].(*agentCall)
		var args map[string]any
		var refusal *command.Refusal
		if !d.on(setting.AgentToolsEnabled) {
			refusal = &command.Refusal{Code: "setting_off", Message: setting.AgentToolsEnabled.Refusal().Error(), Fix: "Use the attn CLI until the setting is on."}
		}
		if refusal == nil {
			args, refusal = def.arguments(req.Params.Arguments, resolved)
		}
		if refusal == nil {
			refusal = d.localWork(def, args, call.caller)
		}
		var result any
		if refusal == nil {
			result, refusal = d.runAgentCommand(ctx, call, def.Command, args)
		}
		if refusal != nil {
			d.logf("agent tools: %s by %s (session %s): refused %s: %s", def.Name, call.who, call.caller.Session, refusal.Code, refusal.Message)
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: def.refusalText(refusal)}}}, nil
		}
		d.logf("agent tools: %s by %s (session %s): ok", def.Name, call.who, call.caller.Session)
		if text, ok := result.(string); ok {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{}, StructuredContent: result}, nil
	}
	return func(s *mcp.Server) { s.AddTool(tool, handler) }
}

func (def agentToolDefinition) arguments(raw json.RawMessage, schema *jsonschema.Resolved) (map[string]any, *command.Refusal) {
	args := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, def.usage(fmt.Sprintf("arguments must be a JSON object: %v", err))
		}
	}
	if err := schema.Validate(args); err != nil {
		return nil, def.usage(err.Error())
	}
	return args, nil
}

func (def agentToolDefinition) usage(message string) *command.Refusal {
	return &command.Refusal{Code: command.CodeUsage, Message: message, Fix: "Call " + def.Name + " with only the arguments its input schema lists, and every one it requires.", Arguments: true}
}

var refusalArgPattern = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`\{([a-z_]+)\}`) })

func (def agentToolDefinition) refusalText(r *command.Refusal) string {
	render := func(text string) string { return refusalArgPattern().ReplaceAllString(text, "$1") }
	text := r.Code + ": " + render(r.Message)
	if r.Fix != "" {
		text += "\nFix: " + render(r.Fix)
	}
	return text
}

func (d *Daemon) runAgentCommand(ctx context.Context, call *agentCall, name string, decoded map[string]any) (any, *command.Refusal) {
	args, err := json.Marshal(decoded)
	if err != nil {
		return nil, command.AsRefusal(err)
	}
	if protocol.CommandRuns[name] == protocol.LocatorHome && !d.enrollmentStatus().IsHome() {
		return d.forwardAgentCommand(call.caller, name, decoded)
	}
	result, refusal := d.commandLayer().Run(ctx, call.caller, name, args)
	if then, ok := result.(command.Then); ok {
		call.after = then.After
		return then.Result, nil
	}
	return result, refusal
}

func (d *Daemon) forwardAgentCommand(c command.Caller, name string, args map[string]any) (any, *command.Refusal) {
	msg := protocol.RunCommandMessage{Cmd: protocol.CmdRunCommand, Command: name, Args: protocol.RecordUnknown(args)}
	if c.Session != "" {
		msg.SessionID = protocol.Ptr(c.Session)
	}
	if c.Profile != "" {
		msg.ProfileID = protocol.Ptr(c.Profile)
	}
	if c.Terminal != "" {
		msg.CallerID = protocol.Ptr(c.Terminal)
	}
	frame, err := json.Marshal(msg)
	if err != nil {
		return nil, command.AsRefusal(err)
	}
	var out bytes.Buffer
	if err := d.router().Forward(append(frame, '\n'), c.Session, nil, &out); err != nil {
		return nil, command.AsRefusal(err)
	}
	var reply protocol.Response
	scanner := bufio.NewScanner(&out)
	scanner.Buffer(nil, protocol.MaxSocketFrameBytes)
	for scanner.Scan() {
		reply = protocol.Response{}
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
			return nil, command.AsRefusal(fmt.Errorf("the home's reply to %s: %w", name, err))
		}
	}
	switch {
	case reply.Ok:
		return reply.CommandResult, nil
	case reply.CommandRefusal != nil:
		return nil, &command.Refusal{Code: reply.CommandRefusal.Code, Message: reply.CommandRefusal.Message, Fix: protocol.Deref(reply.CommandRefusal.Fix)}
	default:
		return nil, &command.Refusal{Code: string(protocol.RefusalUnclassified), Message: protocol.Deref(reply.Error)}
	}
}

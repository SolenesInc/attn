package daemon

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/mcpserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

//go:embed agent_tools_generated.json
var agentToolDefinitions []byte

type agentToolDefinition struct {
	Command      string           `json:"command"`
	Name         string           `json:"name"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	Core         bool             `json:"core"`
	ReadOnly     bool             `json:"read_only"`
	Local        []agentToolLocal `json:"local"`
	InputSchema  json.RawMessage  `json:"inputSchema"`
	OutputSchema json.RawMessage  `json:"outputSchema"`
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
	named  func(meta json.RawMessage) (command.Caller, string, *command.Refusal)
}

type agentToolServers struct {
	once    sync.Once
	servers map[string]*mcpserver.Server
	tools   map[string]agentTool
}

type agentTool struct {
	def   agentToolDefinition
	input *mcpserver.Schema
}

func (d *Daemon) agentToolsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		set := r.URL.Query().Get("tools")
		server, ok := d.agentToolServers()[set]
		if !ok {
			http.Error(w, fmt.Sprintf("tools=%s names no tool set; use tools=core, tools=rest, or leave it out for every tool", set), http.StatusBadRequest)
			return
		}
		call, err := d.agentCallFor(r)
		if err != nil {
			d.logf("agent tools: %s %s refused: %v", r.Method, r.URL.Path, err)
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		server.Serve(w, r, func(ctx context.Context, tool string, arguments, meta json.RawMessage) mcpserver.Result {
			return d.callAgentTool(ctx, call, d.agentToolSet.tools[tool], arguments, meta)
		})
		if call.after != nil {
			_ = http.NewResponseController(w).Flush()
			call.after()
		}
	})
}

func (d *Daemon) agentCallFor(r *http.Request) (*agentCall, error) {
	scheme, token, _ := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return nil, errors.New("no bearer token")
	}
	call, err := d.agentCaller(strings.TrimSpace(token), r.Header)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	return call, nil
}

func (d *Daemon) agentCaller(token string, header http.Header) (*agentCall, error) {
	credential, err := d.agentKey.verify(token)
	if err != nil {
		return nil, err
	}
	if profile, ok := strings.CutPrefix(credential.name, sharedCodexServerPrefix); ok && credential.kind == credentialServer {
		return &agentCall{who: "shared Codex of profile " + profile, named: func(meta json.RawMessage) (command.Caller, string, *command.Refusal) {
			return d.sharedCodexCaller(profile, meta)
		}}, nil
	}
	caller, who, err := d.credentialCaller(credential, header)
	if err != nil {
		return nil, err
	}
	return &agentCall{caller: caller, who: who}, nil
}

func (d *Daemon) sharedCodexCaller(profile string, meta json.RawMessage) (command.Caller, string, *command.Refusal) {
	var named struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(meta, &named)
	conversation := strings.TrimSpace(named.ThreadID)
	if conversation == "" {
		return command.Caller{}, "shared Codex of profile " + profile, &command.Refusal{Code: "no_conversation", Message: "the call's _meta names no Codex conversation in threadId", Fix: "Call the tool from a Codex conversation; Codex names it in _meta.threadId."}
	}
	who := "shared Codex conversation " + conversation
	session, _, _ := d.codexShared().resolveCaller(harness.CodexThreadTerminal(profile, conversation))
	if session == "" {
		return command.Caller{}, who, &command.Refusal{Code: "no_session", Message: fmt.Sprintf("no session of profile %s holds Codex conversation %s", profile, conversation), Fix: "Call the tool from a conversation that an attn session shows."}
	}
	return d.sessionCaller(session), who, nil
}

func (d *Daemon) credentialCaller(credential agentCredential, header http.Header) (command.Caller, string, error) {
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
		session := l.toolsCaller(conversation)
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

type agentToolCatalog struct {
	Name         string                `json:"name"`
	Description  string                `json:"description"`
	Instructions string                `json:"instructions"`
	Tools        []agentToolDefinition `json:"tools"`
}

var agentToolsCatalog = sync.OnceValue(func() agentToolCatalog {
	var catalog agentToolCatalog
	if err := json.Unmarshal(agentToolDefinitions, &catalog); err != nil {
		panic(err)
	}
	return catalog
})

func (d *Daemon) agentToolServers() map[string]*mcpserver.Server {
	d.agentToolSet.once.Do(func() {
		catalog := agentToolsCatalog()
		sets := map[string][]mcpserver.Tool{}
		d.agentToolSet.tools = map[string]agentTool{}
		for _, def := range catalog.Tools {
			d.agentToolSet.tools[def.Name] = agentTool{def: def, input: def.parsedInput()}
			sets[agentToolsAll] = append(sets[agentToolsAll], def.offered())
			if def.Core {
				sets[agentToolsCore] = append(sets[agentToolsCore], def.offered())
			} else {
				sets[agentToolsRest] = append(sets[agentToolsRest], def.offered())
			}
		}
		d.agentToolSet.servers = map[string]*mcpserver.Server{}
		for _, set := range []string{agentToolsAll, agentToolsCore, agentToolsRest} {
			d.agentToolSet.servers[set] = mcpserver.New(catalog.Name, buildinfo.Version, catalog.Instructions, sets[set])
		}
	})
	return d.agentToolSet.servers
}

func (def agentToolDefinition) parsedInput() *mcpserver.Schema {
	input, err := mcpserver.ParseSchema(def.InputSchema)
	if err != nil {
		panic(fmt.Sprintf("%s: input schema: %v", def.Name, err))
	}
	return input
}

func (def agentToolDefinition) offered() mcpserver.Tool {
	tool := mcpserver.Tool{
		Name:         def.Name,
		Title:        def.Title,
		Description:  def.Description,
		InputSchema:  def.InputSchema,
		OutputSchema: def.OutputSchema,
		Annotations:  &mcpserver.Annotations{Title: def.Title, ReadOnlyHint: def.ReadOnly},
	}
	if def.Core {
		tool.Meta = map[string]any{"anthropic/alwaysLoad": true}
	}
	return tool
}

func (d *Daemon) callAgentTool(ctx context.Context, call *agentCall, tool agentTool, arguments, meta json.RawMessage) mcpserver.Result {
	def := tool.def
	var args map[string]any
	var refusal *command.Refusal
	if call.named != nil {
		call.caller, call.who, refusal = call.named(meta)
	}
	if refusal == nil && !d.on(setting.AgentToolsEnabled) {
		refusal = &command.Refusal{Code: "setting_off", Message: setting.AgentToolsEnabled.Refusal().Error(), Fix: "Use the attn CLI until the setting is on."}
	}
	if refusal == nil {
		args, refusal = def.arguments(arguments, tool.input)
	}
	var caller *callerSide
	if refusal == nil {
		caller, refusal = d.doCallerSideWork(ctx, def, args, call.caller)
	}
	var result any
	if refusal == nil {
		result, refusal = d.runAgentCommand(ctx, call, def.Command, args)
		refusal = caller.explainRefusal(refusal)
	}
	if refusal != nil {
		d.logf("agent tools: %s by %s (session %s): refused %s: %s", def.Name, call.who, call.caller.Session, refusal.Code, refusal.Message)
		return mcpserver.Result{IsError: true, Content: []mcpserver.Content{mcpserver.Text(def.refusalText(refusal))}}
	}
	d.logf("agent tools: %s by %s (session %s): ok", def.Name, call.who, call.caller.Session)
	if text, ok := result.(string); ok {
		return mcpserver.Result{Content: []mcpserver.Content{mcpserver.Text(text)}}
	}
	return mcpserver.Result{Content: []mcpserver.Content{}, StructuredContent: result}
}

func (def agentToolDefinition) arguments(raw json.RawMessage, schema *mcpserver.Schema) (map[string]any, *command.Refusal) {
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

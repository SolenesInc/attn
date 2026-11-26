package daemon

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/catalog"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/mcpserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

//go:embed agent_tools_generated.json
var agentToolDefinitions []byte

type agentToolDefinition struct {
	Command     string          `json:"command"`
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Tool        bool            `json:"tool"`
	Signature   string          `json:"signature"`
	Effect      catalog.Effect  `json:"effect"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Receipt     []string        `json:"receipt"`
}

type scriptToolDefinition struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Script      string `json:"script"`
}

const (
	doToolName     = "attn_do"
	recordToolName = "attn_record"
)

type agentCall struct {
	caller command.Caller
	who    string
	after  []func()
	named  func(meta json.RawMessage) (command.Caller, string, *command.Refusal)
}

type agentToolServers struct {
	once         sync.Once
	server       *mcpserver.Server
	tools        map[string]agentTool
	commandNames []string
	script       *mcpserver.Schema
}

type agentTool struct {
	def     agentToolDefinition
	input   *mcpserver.Schema
	receipt catalog.Receipt
}

func (d *Daemon) agentToolsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server := d.agentToolServer()
		call, err := d.agentCallFor(r)
		if err != nil {
			d.logf("agent tools: %s %s refused: %v", r.Method, r.URL.Path, err)
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		started := time.Now()
		method := server.Serve(w, r, func(ctx context.Context, tool string, arguments, meta json.RawMessage) mcpserver.Result {
			return d.callAgentTool(ctx, call, tool, arguments, meta)
		})
		if method == "initialize" || method == "tools/list" {
			d.logf("agent tools: %s for %s (session %s) in %s", method, call.who, call.caller.Session, time.Since(started).Round(time.Microsecond))
		}
		if len(call.after) > 0 {
			_ = http.NewResponseController(w).Flush()
			for _, after := range call.after {
				after()
			}
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
	caller, who, named, err := d.credentialCaller(credential, header)
	if err != nil {
		return nil, err
	}
	if named != nil {
		return &agentCall{who: who, named: named}, nil
	}
	return &agentCall{caller: caller, who: who}, nil
}

type namedCaller func(meta json.RawMessage) (command.Caller, string, *command.Refusal)

func (d *Daemon) credentialCaller(credential agentCredential, header http.Header) (command.Caller, string, namedCaller, error) {
	if credential.kind == credentialTerminal {
		terminal := protocol.TerminalID(credential.name)
		_, _, _, resolved := d.resolveCaller(terminal)
		if !resolved && !d.terminalLive(terminal) {
			return command.Caller{}, "", nil, fmt.Errorf("terminal %s has ended", terminal)
		}
		who := "terminal " + string(terminal)
		session := d.sessionInTerminal(terminal)
		if session == "" {
			return command.Caller{Terminal: terminal}, who, nil, nil
		}
		return d.sessionCaller(session), who, nil, nil
	}
	resolver := d.credentialOwner(credential.name)
	if resolver == nil {
		return command.Caller{}, "", nil, fmt.Errorf("attn holds no server at %s", credential.name)
	}
	session, who, perCall, err := resolver.Authenticate(credential.name, header)
	if err != nil {
		return command.Caller{}, "", nil, err
	}
	if perCall {
		return command.Caller{}, who, func(meta json.RawMessage) (command.Caller, string, *command.Refusal) {
			session, who, refusal := resolver.IdentifyCall(credential.name, meta)
			if refusal != nil {
				return command.Caller{}, who, &command.Refusal{Code: refusal.Code, Message: refusal.Message, Fix: refusal.Fix}
			}
			return d.sessionCaller(session), who, nil
		}, nil
	}
	return d.sessionCaller(session), who, nil, nil
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
	Do           scriptToolDefinition  `json:"do"`
	Record       scriptToolDefinition  `json:"record"`
	Tools        []agentToolDefinition `json:"tools"`
}

var agentToolsCatalog = sync.OnceValue(func() agentToolCatalog {
	var catalog agentToolCatalog
	if err := json.Unmarshal(agentToolDefinitions, &catalog); err != nil {
		panic(err)
	}
	return catalog
})

func (d *Daemon) agentToolServer() *mcpserver.Server {
	set := &d.agentToolSet
	set.once.Do(func() {
		offered := agentToolsCatalog()
		tools := []mcpserver.Tool{offered.Record.offered(recordToolName), offered.Do.offered(doToolName)}
		set.tools = map[string]agentTool{}
		for _, def := range offered.Tools {
			set.tools[def.Name] = agentTool{def: def, input: def.parsedInput(), receipt: catalog.ReceiptOf(def.Receipt)}
			set.commandNames = append(set.commandNames, def.Name)
			if def.Tool {
				tools = append(tools, def.offered())
			}
		}
		set.script = parsedSchema(recordToolName, tools[0].InputSchema)
		set.server = mcpserver.New(offered.Name, buildinfo.Version, offered.Instructions, tools, set.notServed)
	})
	return set.server
}

func (set *agentToolServers) notServed(tool string) string {
	if command, ok := set.tools[tool]; ok {
		runsIt := recordToolName
		if command.def.Effect == catalog.EffectAct {
			runsIt = doToolName
		}
		return fmt.Sprintf("attn's commands are functions of a script. Call %[1]s with the script %[2]s({...}); skill({command: %[2]q}) prints its arguments.", runsIt, tool)
	}
	return "attn serves attn_record, attn_do, ask and skill. attn_record and attn_do run a script of attn's commands; skill() prints what attn can do and skill({command: \"seed\"}) a family's functions."
}

func parsedSchema(tool string, raw json.RawMessage) *mcpserver.Schema {
	schema, err := mcpserver.ParseSchema(raw)
	if err != nil {
		panic(fmt.Sprintf("%s: input schema: %v", tool, err))
	}
	return schema
}

func (def agentToolDefinition) parsedInput() *mcpserver.Schema {
	return parsedSchema(def.Name, def.InputSchema)
}

var inEveryRequest = map[string]any{"anthropic/alwaysLoad": true}

func (def scriptToolDefinition) offered(name string) mcpserver.Tool {
	input, err := json.Marshal(map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"script": map[string]any{"type": "string", "description": def.Script}},
		"required":             []string{"script"},
		"additionalProperties": false,
	})
	if err != nil {
		panic(fmt.Sprintf("%s: input schema: %v", name, err))
	}
	return mcpserver.Tool{
		Name: name, Title: def.Title, Description: def.Description, InputSchema: input, Meta: inEveryRequest,
		Annotations: &mcpserver.Annotations{Title: def.Title},
	}
}

func (def agentToolDefinition) offered() mcpserver.Tool {
	return mcpserver.Tool{
		Name: def.Name, Title: def.Title, Description: def.Description, InputSchema: def.InputSchema, Meta: inEveryRequest,
		Annotations: &mcpserver.Annotations{Title: def.Title, ReadOnlyHint: def.Effect == catalog.EffectRead},
	}
}

func (d *Daemon) callAgentTool(ctx context.Context, call *agentCall, name string, arguments, meta json.RawMessage) mcpserver.Result {
	refusal := d.refusesAgentCall(call, meta)
	call.caller.ThroughTools = true
	if most, script := scriptToolEffects[name]; refusal == nil && script {
		var in struct {
			Script string `json:"script"`
		}
		if refusal = checkArguments(name, arguments, d.agentToolSet.script); refusal == nil {
			if err := json.Unmarshal(arguments, &in); err == nil {
				return d.runAgentScript(ctx, call, scriptTool{name: name, most: most}, in.Script)
			}
		}
	}
	if refusal != nil {
		d.logAgentCommand(call, name, refusal)
		return refusedResult(refusal)
	}
	return d.callCommandTool(ctx, call, d.agentToolSet.tools[name], arguments)
}

func (d *Daemon) refusesAgentCall(call *agentCall, meta json.RawMessage) *command.Refusal {
	if call.named != nil {
		var refusal *command.Refusal
		if call.caller, call.who, refusal = call.named(meta); refusal != nil {
			return refusal
		}
	}
	if !d.on(setting.AgentToolsEnabled) {
		return &command.Refusal{Code: "setting_off", Message: setting.AgentToolsEnabled.Refusal().Error(), Fix: "Use the attn CLI until the setting is on."}
	}
	if call.caller.Session == "" {
		return &command.Refusal{Code: command.CodeNoSession, Message: call.who + " is not showing a session"}
	}
	return nil
}

func (d *Daemon) callCommandTool(ctx context.Context, call *agentCall, tool agentTool, arguments json.RawMessage) mcpserver.Result {
	refusal := tool.withRules(checkArguments(tool.def.Name, arguments, tool.input))
	var result any
	if refusal == nil {
		result, refusal = d.runAgentCommand(ctx, call.caller, tool.def.Command, arguments)
		refusal = tool.withRules(refusal)
	}
	if then, ok := result.(command.Then); ok {
		result = then.Result
		if then.After != nil {
			call.after = append(call.after, then.After)
		}
	}
	d.logAgentCommand(call, tool.def.Name, refusal)
	if refusal != nil {
		return refusedResult(refusal)
	}
	answer := scriptAnswer(tool, result)
	if answer.JSON != "" {
		answer.Text = tool.json(tool.answer(result))
	}
	return mcpserver.Result{Content: []mcpserver.Content{mcpserver.Text(answer.Text)}}
}

func refusedResult(refusal *command.Refusal) mcpserver.Result {
	return mcpserver.Result{IsError: true, Content: []mcpserver.Content{mcpserver.Text(refusalText(refusal))}}
}

const loggedTextChars = 1_000 // The longest of 364 refusals in the agent tools' wire tests was 357.

func (d *Daemon) logAgentCommand(call *agentCall, name string, refusal *command.Refusal) {
	if refusal != nil {
		d.logf("agent tools: %s by %s (session %s): refused %s: %s", name, call.who, call.caller.Session, refusal.Code, cutEnd(refusal.Message, loggedTextChars))
		return
	}
	d.logf("agent tools: %s by %s (session %s): ok", name, call.who, call.caller.Session)
}

func (tool agentTool) json(answer any) string {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(answer); err != nil {
		panic(fmt.Sprintf("%s: result: %v", tool.def.Name, err))
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func (tool agentTool) answer(result any) any {
	if tool.receipt.Whole() {
		return result
	}
	data, err := json.Marshal(result)
	if err != nil {
		panic(fmt.Sprintf("%s: result: %v", tool.def.Name, err))
	}
	var whole any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&whole); err != nil {
		panic(fmt.Sprintf("%s: result: %v", tool.def.Name, err))
	}
	return tool.receipt.Of(whole)
}

func checkArguments(name string, raw json.RawMessage, schema *mcpserver.Schema) *command.Refusal {
	args := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return argumentRefusal(name, fmt.Sprintf("arguments must be a JSON object: %v", err))
		}
	}
	if err := schema.Validate(args); err != nil {
		return argumentRefusal(name, err.Error())
	}
	return nil
}

func argumentRefusal(name, message string) *command.Refusal {
	return &command.Refusal{Code: command.CodeUsage, Message: message, Fix: "Call " + name + " with only the arguments it takes, and every one it requires.", Arguments: true}
}

var toolCatalog = sync.OnceValue(func() *catalog.Catalog {
	c, err := catalog.Builtin()
	if err != nil {
		panic(fmt.Sprintf("agent tools: catalog: %v", err))
	}
	return c
})

func refusalText(r *command.Refusal) string {
	text := r.Code + ": " + toolCatalog().ToolText(r.Message)
	if r.Fix != "" {
		text += "\nFix: " + toolCatalog().ToolText(r.Fix)
	}
	return text
}

func (d *Daemon) runAgentCommand(ctx context.Context, caller command.Caller, name string, args json.RawMessage) (any, *command.Refusal) {
	if protocol.CommandRuns[name] == protocol.LocatorHome && !d.enrollmentStatus().IsHome() {
		return d.forwardCommand(ctx, caller, name, args)
	}
	return d.commandLayer().Run(ctx, caller, name, args)
}

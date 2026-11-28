// Package plugin is the harness of every agent a plugin registers with attn.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/transcript"
)

const callTimeout = 30 * time.Second
const deliverTimeout = 15 * time.Second

// Full header scans: 91 real files <=125ms; 10,000 synthetic files across two directories <=7.3s.
const resumeAvailabilityTimeout = 10 * time.Second

// Source is the plugin harnesses: one for each agent a connected plugin registered.
type Source struct {
	host    harness.Host
	servers *appserver.Client
	plugins harness.PluginHost
}

func New(host harness.Host, servers *appserver.Client, plugins harness.PluginHost) *Source {
	return &Source{host: host, servers: servers, plugins: plugins}
}

// Harnesses are the harnesses of the registered agents.
func (s *Source) Harnesses() []harness.Harness {
	registered := s.plugins.Drivers()
	all := make([]harness.Harness, 0, len(registered))
	for _, reg := range registered {
		h := Harness{src: s, reg: reg}
		if l := s.servers.Link(reg.Agent); l != nil && l.Can(appserver.AbilityTargets) {
			all = append(all, targetsHarness{Harness: h, link: l})
			continue
		}
		all = append(all, h)
	}
	return all
}

// Unconnected is the harness of an agent no connected plugin registers.
func (s *Source) Unconnected(id harness.ID) harness.Harness {
	return Harness{src: s, reg: harness.PluginDriver{Agent: id}}
}

// Harness is one agent a plugin registered.
type Harness struct {
	src *Source
	reg harness.PluginDriver
}

type targetsHarness struct {
	Harness
	link *appserver.Link
}

func (h targetsHarness) Targets(ctx context.Context, cwd string) ([]protocol.ExecutionTarget, error) {
	return h.link.Targets(ctx, cwd)
}

func (h Harness) link() *appserver.Link { return h.src.servers.Link(h.reg.Agent) }

func (h Harness) displayName() string {
	if h.reg.DisplayName != "" {
		return h.reg.DisplayName
	}
	return h.reg.Agent
}

func route(reg harness.PluginDriver) string {
	if reg.Capabilities["message_delivery"] || reg.Capabilities["server_binding"] {
		return "direct"
	}
	return "terminal"
}

func (h Harness) Facts() harness.Facts {
	declared := h.reg.Capabilities
	facts := harness.Facts{
		ID:          h.reg.Agent,
		DisplayName: h.displayName(),
		Logo:        h.reg.Logo,
		Options: harness.Options{
			Resume:             declared["resume"],
			Yolo:               declared["yolo"],
			InitialPrompt:      declared["initial_prompt"],
			LaunchInstructions: declared["launch_instructions"],
			ModelPin:           declared["model_pin"],
			EffortPin:          declared["effort_pin"],
		},
		SkillDirs:      h.reg.SkillDirs,
		CacheTTL:       time.Hour,
		Signals:        signals(h.reg),
		Recovery:       h.recovery(),
		ModelDiscovery: declared["model_discovery"] || declared["server_binding"],
		Activity:       h.link().WatchesConversations(),
		AgentTools:     declared["agent_tools"] && !declared["server_binding"],
		Owner:          h.reg.Plugin,
	}
	if l := h.link(); l != nil {
		facts.ServerTerminal = l.ServerTerminal
	}
	return facts
}

func signals(reg harness.PluginDriver) harness.Signals {
	declared := reg.Capabilities
	s := harness.Signals{Heartbeat: 500 * time.Millisecond, ReviewerDwell: time.Minute, ReportsTools: declared["pull_request_reporting"], ReportsBackgroundTasks: false, ReportsSteps: false, StopsReadable: true, StopsClassified: true}
	if declared["state_reporting"] {
		s.StateAuthority = harness.StateByHarness
	}
	s.LaunchesWorking = reg.Plugin != "" && !declared["state_reporting"] && !declared["server_binding"]
	return s
}

func (h Harness) recovery() harness.Recovery {
	if h.link() != nil {
		return harness.ByServer
	}
	return harness.ByTerminal
}

func (h Harness) Plan(req harness.LaunchRequest) (harness.Planned, error) {
	if req.Reason == harness.Reload && !h.reg.Capabilities["resume"] {
		return harness.Planned{}, fmt.Errorf("agent %q requires the resume capability to reload", h.reg.Agent)
	}
	planned := h.link().Plan(h.reg.Plugin, req)
	if h.reg.Capabilities["server_binding"] {
		planned.Conversation = harness.ResumedOrTerminal(req)
	}
	return planned, nil
}

func (h Harness) Launch(_ context.Context, req harness.LaunchRequest) (harness.Command, error) {
	reg, ok := h.src.plugins.Driver(h.reg.Agent)
	if !ok {
		return harness.Command{}, fmt.Errorf("agent %q is not available on this machine", h.reg.Agent)
	}
	if reg.Plugin != req.Owner {
		return harness.Command{}, fmt.Errorf("agent %q here is provided by plugin %q, not %q", reg.Agent, reg.Plugin, req.Owner)
	}
	params := spawnParams{
		Executable:      explicitExecutable(req),
		Agent:           reg.Agent,
		TerminalID:      protocol.TerminalID(req.Terminal),
		RunID:           req.Lifecycle,
		CWD:             req.Dir,
		Label:           req.Label,
		Yolo:            req.Options.Yolo,
		Model:           req.Options.Model,
		Effort:          req.Options.Effort,
		ExecutionTarget: req.Options.Target,
		InitialPrompt:   req.InitialPrompt,
		Metadata:        req.Note,
		ResumeSessionID: strings.TrimSpace(req.Conversation),
	}
	if params.Executable == "" {
		params.Executable = strings.TrimSpace(h.src.host.Setting(setting.Executable.For(reg.Agent)))
	}
	if req.Lifetime == harness.InServer {
		params.ConversationID, _ = harness.ResumedOrTerminal(req).Recorded()
	}
	serverBound := reg.Capabilities["server_binding"]
	var server *appserver.Link
	if serverBound {
		server = h.src.servers.Link(reg.Agent)
		params.Executable = server.Executable(req)
		if err := server.Launch(req); err != nil {
			return harness.Command{}, err
		}
		if reg.Capabilities["agent_tools"] {
			if tools := server.SharedTools(); tools != nil {
				params.AgentTools = &agentTools{Name: tools.Name, Description: tools.Description}
			}
		}
	} else {
		if reg.Capabilities["agent_tools"] {
			if tools, offered := h.src.host.TerminalTools(req.Terminal); offered {
				params.AgentTools = &agentTools{URL: tools.URL, Credential: tools.Credential, Description: tools.Description}
			}
		}
		h.src.host.Sessions().BeginLaunch(req.Session, reg.Plugin, params.RunID, route(reg))
	}
	if reg.Capabilities["launch_instructions"] {
		guidance := req.Guidance
		guidance.AgentTools = params.AgentTools != nil
		params.Instructions = &launchInstructions{Kind: "agent", Content: guidance.Instructions(), ProfileID: req.Profile}
	}
	resume := req.Reason == harness.Reload || req.Reason == harness.Resume && reg.Capabilities["resume"]
	if serverBound && !resume {
		params.ResumeSessionID = ""
	}
	result, err := h.spawn(reg, params, resume)
	if err != nil {
		return harness.Command{}, err
	}
	env, err := commandEnv(result.Env)
	if err != nil {
		return harness.Command{}, err
	}
	if serverBound {
		env = append(env, server.HomeEnv())
	}
	return harness.Command{Argv: append([]string(nil), result.Argv...), Env: env, Dir: strings.TrimSpace(result.CWD)}, nil
}

func explicitExecutable(req harness.LaunchRequest) string {
	if !req.Unattended.IsZero() {
		return req.Unattended.Executable
	}
	return req.Executable
}

func (h Harness) spawn(reg harness.PluginDriver, params spawnParams, resume bool) (spawnResult, error) {
	if err := (Harness{src: h.src, reg: reg}).Facts().Accept(harness.Request{Yolo: params.Yolo, Model: params.Model != "", Effort: params.Effort != ""}); err != nil {
		return spawnResult{}, err
	}
	if resume && !reg.Capabilities["resume"] {
		return spawnResult{}, fmt.Errorf("agent %q does not support resume", reg.Agent)
	}
	if params.ResumeSessionID != "" && !reg.Capabilities["resume"] {
		return spawnResult{}, fmt.Errorf("agent %q cannot pick up conversation %s: its driver does not support resume", reg.Agent, params.ResumeSessionID)
	}
	method := "driver.spawn"
	if resume {
		method = "driver.resume"
	} else {
		params.ResumeSessionID = ""
	}
	var result spawnResult
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if err := h.src.plugins.Call(ctx, reg.Plugin, method, params, &result); err != nil {
		return spawnResult{}, err
	}
	if len(result.Argv) == 0 || strings.TrimSpace(result.Argv[0]) == "" {
		return spawnResult{}, fmt.Errorf("plugin %q returned an empty argv", reg.Plugin)
	}
	return result, nil
}

func commandEnv(values map[string]string) ([]string, error) {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("plugin driver returned invalid environment key %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env, nil
}

func (Harness) Start(context.Context) error { return nil }

func (h Harness) Recover(ctx context.Context, point harness.RecoverPoint) {
	h.link().Recover(ctx, point)
}

func (Harness) SettingChanged(string) {}

func (h Harness) Released(string) { h.link().Release() }

func (Harness) Stop() {}

func (Harness) Terminal(harness.TerminalID, harness.TerminalEvent) {}

func (h Harness) End(_ context.Context, e harness.Ending) {
	if l := h.link(); l != nil {
		l.SessionClosed(e.Session, e.Profile, e.Released)
	}
}

// RunEnded tells the plugin the run ended, addressing it by the terminal the driver launched it in.
func (h Harness) RunEnded(e harness.RunEnd) {
	plugin, terminal, run := e.Owner, e.Terminal, e.Run
	host := h.src.host
	if _, _, connected := h.src.plugins.Health(plugin); !connected {
		host.Logf("plugin session close notification dropped: plugin=%s session=%s run=%s owner disconnected", plugin, terminal, run)
		return
	}
	params := sessionClosedParams{TerminalID: protocol.TerminalID(terminal), RunID: run, Reason: strings.TrimSpace(string(e.Reason)), ExitCode: e.ExitCode, Signal: strings.TrimSpace(e.Signal)}
	host.Life().Go("notifyPluginSessionClosed", func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		var result sessionClosedResult
		if err := h.src.plugins.Call(ctx, plugin, "driver.session_closed", params, &result); err != nil {
			host.Logf("plugin session close notification failed: plugin=%s session=%s run=%s err=%v", plugin, terminal, run, err)
			return
		}
		host.Logf("plugin session close notified: plugin=%s session=%s run=%s reason=%s", plugin, terminal, run, params.Reason)
	})
}

func (h Harness) Route(facts harness.SessionFacts) (string, bool) {
	reg, ok := h.src.ownedDriver(facts.Harness, facts.RunOwner)
	if !ok {
		return "", false
	}
	return route(reg), true
}

func (s *Source) ownedDriver(agent harness.ID, owner string) (harness.PluginDriver, bool) {
	reg, ok := s.plugins.Driver(agent)
	if !ok || reg.Plugin != owner {
		return harness.PluginDriver{}, false
	}
	if _, _, connected := s.plugins.Health(owner); !connected {
		return harness.PluginDriver{}, false
	}
	return reg, true
}

func (h Harness) Models(ctx context.Context) (harness.Models, error) {
	if server := h.link(); server != nil {
		return server.Models(ctx, false)
	}
	if h.reg.Plugin == "" {
		return harness.Models{}, fmt.Errorf("harness %q is not available", h.reg.Agent)
	}
	if !h.reg.Capabilities["model_discovery"] {
		return harness.NoModelDiscovery, nil
	}
	var reported struct {
		Models []protocol.HarnessModel `json:"models"`
		Detail string                  `json:"detail"`
	}
	if err := h.src.plugins.Call(ctx, h.reg.Plugin, "driver.models", map[string]string{"agent": h.reg.Agent}, &reported); err != nil {
		return harness.Models{}, err
	}
	return harness.Models{List: reported.Models, Detail: reported.Detail}, nil
}

func (h Harness) Health(context.Context) harness.Health {
	status, detail, connected := h.src.plugins.Health(h.reg.Plugin)
	if !connected {
		return harness.Health{Installation: harness.Missing, Offered: true, Reason: "plugin " + h.reg.Plugin + " is not connected"}
	}
	installation := harness.Unknown
	switch status {
	case harness.HealthHealthy:
		installation = harness.Installed
	case harness.HealthUnhealthy:
		installation = harness.Missing
	}
	return harness.Health{Installation: installation, Offered: true, Reason: detail}
}

func (h Harness) Conversations() harness.ConversationStore {
	return conversations{src: h.src, plugin: h.reg.Plugin}
}

type conversations struct {
	src    *Source
	plugin string
}

func (c conversations) watching(ref harness.ConversationRef) *appserver.Link {
	if l := c.src.servers.Link(string(ref.Harness)); l.WatchesConversations() {
		return l
	}
	return nil
}

func (c conversations) Admit(ref harness.ConversationRef) error {
	return c.src.servers.Link(string(ref.Harness)).Admit(ref)
}

func (conversations) Prepare(context.Context, harness.ConversationRef) (bool, error) {
	return true, nil
}

func (c conversations) Check(_ context.Context, refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	availability := make(map[harness.ConversationRef]harness.Availability, len(refs))
	onServer := map[*appserver.Link][]harness.ConversationRef{}
	var asked []harness.ConversationRef
	for _, ref := range refs {
		reg, ok := c.src.plugins.Driver(string(ref.Harness))
		switch l := c.src.servers.Link(reg.Agent); {
		case !ok:
			availability[ref] = harness.Availability{Reason: fmt.Sprintf("agent %q is not installed here, so its conversation cannot be resumed", ref.Harness)}
		case !reg.Capabilities["resume"]:
			availability[ref] = harness.Availability{Reason: fmt.Sprintf("agent %q does not resume conversations, so conversation %s cannot be picked up", ref.Harness, ref.ID)}
		case ref.DirMissing && reg.Capabilities["resume_availability"]:
			availability[ref] = harness.Availability{Resumable: true}
		case l.WatchesConversations():
			onServer[l] = append(onServer[l], ref)
		case !reg.Capabilities["resume_availability"]:
			availability[ref] = harness.Availability{Resumable: true}
		default:
			asked = append(asked, ref)
		}
	}
	for l, refs := range onServer {
		maps.Copy(availability, l.ConversationsResumable(refs))
	}
	if len(asked) > 0 {
		maps.Copy(availability, c.resumable(asked))
	}
	return availability
}

func (c conversations) resumable(refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	asked := make([]resumeConversation, len(refs))
	for i, ref := range refs {
		asked[i] = resumeConversation{Agent: string(ref.Harness), CWD: ref.Dir, ResumeSessionID: ref.ID}
	}
	ctx, cancel := context.WithTimeout(context.Background(), resumeAvailabilityTimeout)
	defer cancel()
	var result resumeAvailableResult
	err := c.src.plugins.Call(ctx, c.plugin, "driver.resume_available", resumeAvailableParams{Conversations: asked}, &result)
	reported := make(map[resumeConversation]harness.Availability, len(result.Availability))
	for _, answer := range result.Availability {
		reported[answer.resumeConversation] = harness.Availability{Resumable: answer.Available, Reason: answer.Reason}
	}
	availability := make(map[harness.ConversationRef]harness.Availability, len(refs))
	for i, ref := range refs {
		conversation := asked[i]
		answer, found := reported[conversation]
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			answer = harness.Availability{CheckFailed: true, Reason: fmt.Sprintf("cannot check conversation %s in %s's storage: resume_availability_timeout=%s exceeded; the driver did not answer", conversation.ResumeSessionID, conversation.Agent, resumeAvailabilityTimeout)}
		case err != nil:
			answer = harness.Availability{CheckFailed: true, Reason: fmt.Sprintf("cannot check conversation %s in %s's storage: %v", conversation.ResumeSessionID, conversation.Agent, err)}
		case !found:
			answer = harness.Availability{Reason: fmt.Sprintf("driver %s omitted resume availability for conversation %s in %s", conversation.Agent, conversation.ResumeSessionID, conversation.CWD)}
		case !answer.Resumable && strings.TrimSpace(answer.Reason) == "":
			answer.Reason = fmt.Sprintf("conversation %s is no longer in %s's storage", conversation.ResumeSessionID, conversation.Agent)
		}
		availability[ref] = answer
	}
	return availability
}

func (c conversations) Page(ctx context.Context, ref harness.ConversationRef, cursor string, limit int) (transcript.EventPage, error) {
	l := c.watching(ref)
	switch {
	case l == nil:
		return c.src.host.Files().Page(ctx, ref, cursor, limit)
	case ref.ID == "":
		return transcript.EventPage{}, harness.ErrTranscriptUnavailable
	}
	return l.TranscriptPage(ctx, ref.ID, cursor, limit)
}

func (c conversations) LastReply(ctx context.Context, ref harness.ConversationRef, max int) (string, error) {
	l := c.watching(ref)
	switch {
	case l == nil:
		return c.src.host.Files().LastReply(ctx, ref, max)
	case ref.ID == "":
		return "", nil
	}
	message, err := l.LastReply(ctx, ref.ID)
	if runes := []rune(message); len(runes) > max {
		message = string(runes[len(runes)-max:])
	}
	return message, err
}

func (c conversations) Window(ctx context.Context, ref harness.ConversationRef) (harness.AssistantWindow, bool) {
	l := c.watching(ref)
	switch {
	case l == nil:
		return c.src.host.Files().Window(ctx, ref)
	case ref.ID == "":
		return harness.AssistantWindow{}, false
	}
	return l.AssistantWindow(ctx, ref.ID)
}

func (c conversations) Activity(ctx context.Context, ref harness.ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error) {
	l := c.watching(ref)
	switch {
	case l == nil:
		return c.src.host.Files().Activity(ctx, ref, kind, cursor)
	case ref.ID == "":
		return protocol.TranscriptActivityResult{}, nil
	}
	return l.Activity(ctx, ref.ID, kind, cursor)
}

func (c conversations) Version(ref harness.ConversationRef) string {
	return c.src.servers.Link(string(ref.Harness)).Version()
}

func (conversations) Locate(ref harness.ConversationRef) string { return ref.Path }

func (conversations) IDFromPath(string) string { return "" }

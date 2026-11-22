package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
)

type adapterHost struct {
	d     *Daemon
	owner string
}

func (d *Daemon) host(owner string) harness.Host { return adapterHost{d: d, owner: owner} }

func (h adapterHost) Life() harness.Life                { return &h.d.life }
func (h adapterHost) Logf(format string, args ...any)   { h.d.logf(format, args...) }
func (h adapterHost) Events() harness.Events            { return h.d.events() }
func (h adapterHost) Setting(s *setting.Setting) string { return h.d.setting(s) }
func (h adapterHost) LoginEnv() []string                { return h.d.latestLoginEnv() }
func (h adapterHost) Sessions() harness.HostSessions    { return hostSessions(h) }
func (h adapterHost) Terminals() harness.HostTerminals  { return hostTerminals(h) }
func (h adapterHost) Records() harness.ServerRecords    { return hostRecords(h) }

func (h adapterHost) ServerTools(subject string) (harness.AgentTools, bool) {
	tools := h.d.agentToolsFor(agentCredential{kind: credentialServer, name: subject})
	if tools == nil {
		return harness.AgentTools{}, false
	}
	catalog := agentToolsCatalog()
	return harness.AgentTools{URL: tools.URL, Credential: tools.Credential, Name: catalog.Name, Description: catalog.Description, Instructions: catalog.Instructions}, true
}

func (h adapterHost) TerminalTools(t harness.TerminalID) (harness.AgentTools, bool) {
	tools := h.d.agentTools(t)
	if tools == nil {
		return harness.AgentTools{}, false
	}
	catalog := agentToolsCatalog()
	return harness.AgentTools{URL: tools.URL, Credential: tools.Credential, Name: catalog.Name, Description: catalog.Description, Instructions: catalog.Instructions}, true
}

func (h adapterHost) ExecutableHealth(facts harness.Facts, offered bool) harness.Health {
	return h.d.executableHealth(facts, offered)
}

type hostSessions adapterHost

func (h hostSessions) Get(id harness.SessionID) *protocol.Session { return h.d.store.Get(id) }
func (h hostSessions) List() []*protocol.Session                  { return h.d.store.List("") }
func (h hostSessions) Holding(conversation string) []harness.SessionID {
	return h.d.store.OpenSessionsHolding(conversation)
}
func (h hostSessions) LaunchedAt(id harness.SessionID) time.Time {
	return h.d.store.SessionLaunchedAt(id)
}
func (h hostSessions) Lifetime(id harness.SessionID) (harness.ID, harness.Lifetime) {
	agent, lifetime := h.d.store.SessionLifetime(id)
	return harness.ID(agent), lifetime
}
func (h hostSessions) Conversation(id harness.SessionID) string { return h.d.serverConversation(id) }
func (h hostSessions) Remote(id harness.SessionID) bool         { return h.d.sessionHost(id) != "" }
func (h hostSessions) PrimaryTerminal(id harness.SessionID) harness.TerminalID {
	return h.d.primaryTerminal(id)
}

func (h hostSessions) Evidence(id harness.SessionID) (harness.SessionEvidence, bool) {
	monitor := h.d.sessionMonitor(id)
	evidence, ok := monitor.Evidence()
	if !ok {
		return harness.SessionEvidence{}, false
	}
	return harness.SessionEvidence{
		TurnOpen:           evidence.TurnOpen,
		ToolOpen:           evidence.ToolOpen,
		Compacting:         evidence.Compacting,
		TurnEnded:          turnHasEnded(monitor),
		ObservedCompletion: monitor.ObservedServerCompletion(),
	}, true
}

func (h hostSessions) TraceClaim(id harness.SessionID, quiet bool, status string, at time.Time) {
	source := sessionmonitor.StateSourceServerReport
	if quiet {
		source = sessionmonitor.StateSourceServerQuiet
	}
	h.d.traceStateEvidence(id, sessionmonitor.Origin{Source: source, ObservedAt: at}, status)
}

type hostTerminals adapterHost

func (h hostTerminals) Live(ctx context.Context) map[harness.TerminalID]struct{} {
	return h.d.liveTerminals(ctx)
}

func (h hostTerminals) Start(ctx context.Context, spec harness.TerminalSpec) error {
	loginEnv, err := h.d.launchEnv(ctx)
	if err != nil {
		return err
	}
	env := append([]string{"ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_AGENT=", "ATTN_CODEX_SHARED_PROFILE=", "CODEX_THREAD_ID="}, spec.Env...)
	return h.d.startAdapterTerminal(ctx, h.owner, ptybackend.SpawnOptions{
		ID: spec.ID, Agent: string(spec.ID), Label: spec.Label, CWD: spec.Dir, Cols: 80, Rows: 24,
		Executable:    spec.Executable,
		AgentArgv:     spec.Argv,
		AgentEnv:      env,
		LoginShellEnv: loginEnv,
		DaemonEnv:     h.d.spawnRoutingEnv(),
	})
}

func (h hostTerminals) Adopt(ctx context.Context, t harness.TerminalID) {
	h.d.adoptAdapterTerminal(ctx, h.owner, t)
}

func (h hostTerminals) Owned(t harness.TerminalID) bool {
	owner, _ := h.d.adapterTerminalOwner(t)
	return owner == h.owner
}

func (h hostTerminals) Remove(ctx context.Context, t harness.TerminalID) error {
	h.d.forgetAdapterTerminal(t)
	if err := h.d.removeTerminalRuntime(ctx, t); err != nil && !errors.Is(err, pty.ErrSessionNotFound) && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type hostRecords adapterHost

func (h hostRecords) TerminalViews(kind string) ([]harness.TerminalView, error) {
	stored, err := h.d.store.TerminalViews(kind)
	views := make([]harness.TerminalView, 0, len(stored))
	for _, v := range stored {
		views = append(views, harness.TerminalView{Terminal: harness.TerminalID(v.TerminalID), Kind: v.Link, Profile: v.ProfileID})
	}
	return views, err
}

func (h hostRecords) SaveTerminalView(v harness.TerminalView) error {
	return h.d.store.SaveTerminalView(store.TerminalView{TerminalID: string(v.Terminal), Link: v.Kind, ProfileID: v.Profile})
}

func (h hostRecords) DeleteTerminalView(t harness.TerminalID) error {
	return h.d.store.DeleteTerminalView(string(t))
}

func (h hostRecords) Feed(id harness.SessionID) harness.FeedRecord {
	feed := h.d.store.ServerFeed(id)
	return harness.FeedRecord{Entry: feed.Entry, UsageBaseline: feed.UsageBaseline, Completion: h.d.store.SessionRuntimeRecords(id).ServerCompletion}
}

func (h hostRecords) SaveFeedEntry(id harness.SessionID, entry int64) {
	h.d.store.SaveServerFeedEntry(id, entry)
}
func (h hostRecords) SaveUsageBaseline(id harness.SessionID, baseline string) {
	h.d.store.SaveServerUsageBaseline(id, baseline)
}
func (h hostRecords) ConversationSeen(conversation string) bool {
	return h.d.store.ServerConversationSeen(conversation)
}

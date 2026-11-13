package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
)

const (
	serverStartLimit = 30 * time.Second
	serverCallLimit  = 5 * time.Second
)

type serverLink struct {
	d *Daemon
	b *serverBinding
}

type serverTree struct {
	Tag          string       `json:"_tag"`
	Conversation string       `json:"conversation"`
	Status       string       `json:"status"`
	Ended        *serverEnded `json:"ended"`
	Trees        []serverTree `json:"trees"`
}

type serverEnded struct {
	Completion int64  `json:"completion"`
	Outcome    string `json:"outcome"`
	Message    string `json:"message"`
	Said       string `json:"said"`
}

func newServerLink(d *Daemon, b *serverBinding) *serverLink { return &serverLink{d: d, b: b} }

func (l *serverLink) logf(format string, args ...any) {
	l.d.logf(l.b.agent+": "+format, args...)
}

func (l *serverLink) kind() string { return l.b.kind }

func (l *serverLink) canRun(req *spawnRequest) bool {
	return req.agent == l.b.agent && (!req.hasPluginDriver || l.b.plugin == req.pluginDriver.PluginName) && !req.isShell && req.policy.unattendedLaunch.Executable == ""
}

func (l *serverLink) enabledForNewLaunches() bool {
	reg, conn := l.d.ensurePluginRegistry().driverConnection(l.b.agent, l.b.plugin)
	return l.b.declaration.Load() != nil && conn != nil && reg.Capabilities["server_binding"]
}

func (l *serverLink) admit(profile, conversation string) error {
	for _, holder := range l.d.store.OpenSessionsHolding(conversation) {
		if _, server := l.d.linkOf(holder).(*serverLink); !server {
			continue
		}
		if session := l.d.store.Get(holder); session != nil {
			if session.ProfileID != profile {
				return fmt.Errorf("conversation %s is open in a session of profile %s; close it there first", conversation, session.ProfileID)
			}
			if l.d.store.SessionLink(holder) != l.b.kind {
				return fmt.Errorf("conversation %s is owned by harness %s; close its session before using harness %s", conversation, session.Agent, l.b.agent)
			}
		}
	}
	return nil
}

func (l *serverLink) prepareLaunch(opts *ptybackend.SpawnOptions, profile string) error {
	if _, err := l.ensureServer(l.d.life.Context(), opts); err != nil {
		return err
	}
	return l.d.store.SaveTerminalView(store.TerminalView{TerminalID: string(opts.ID), Link: l.b.kind, ProfileID: profile})
}

func (l *serverLink) relaunch(opts *ptybackend.SpawnOptions) error {
	if _, err := l.ensureServer(l.d.life.Context(), opts); err != nil {
		l.logf("relaunching terminal %s before its server answers: %v", opts.ID, err)
	}
	return nil
}

func (l *serverLink) resolveCaller(t protocol.TerminalID) (protocol.SessionID, harness.TerminalID, bool) {
	conversation, ok := l.b.conversationOf(harness.TerminalID(t))
	if !ok {
		return "", "", false
	}
	session := l.holder(conversation)
	if session == "" {
		return "", "", true
	}
	return session, l.d.primaryTerminal(session), true
}

func (l *serverLink) runsLink(t harness.TerminalID) bool { return t == l.b.terminal }

func (l *serverLink) serverTerminal(string) harness.TerminalID { return l.b.terminal }

func (l *serverLink) renamed(protocol.SessionID, string) {}

func (l *serverLink) restore(string, string) error { return nil }

func (l *serverLink) transcriptPath(path string) string { return path }

func (l *serverLink) terminalDropped(t harness.TerminalID) {
	if err := l.d.store.DeleteTerminalView(string(t)); err != nil {
		l.logf("%v", err)
	}
}

func (l *serverLink) recoverViews(ctx context.Context) {
	views, err := l.d.store.TerminalViews(l.b.kind)
	if err != nil {
		l.logf("%v", err)
		return
	}
	live := l.d.liveTerminals(ctx)
	for _, view := range views {
		if _, running := live[harness.TerminalID(view.TerminalID)]; !running {
			l.terminalDropped(harness.TerminalID(view.TerminalID))
		}
	}
}

type serverProviderModels struct {
	Provider string `json:"provider"`
	Models   []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Efforts     []string `json:"efforts"`
	} `json:"models"`
}

func (l *serverLink) models(ctx context.Context) ([]protocol.HarnessModel, error) {
	p, err := l.connection()
	if err != nil {
		return nil, err
	}
	c, release, err := p.borrow(ctx, l, true)
	if err != nil {
		return nil, err
	}
	defer release()
	var providers []serverProviderModels
	if err := c.Call(ctx, "models/list", map[string]any{}, &providers); err != nil {
		return nil, err
	}
	var models []protocol.HarnessModel
	for _, p := range providers {
		for _, m := range p.Models {
			effort := protocol.ModelCapabilitySupportUnsupported
			if len(m.Efforts) > 0 {
				effort = protocol.ModelCapabilitySupportSupported
			}
			models = append(models, protocol.HarnessModel{Harness: l.b.agent, Provider: p.Provider, ID: m.ID, Name: m.Name, Description: m.Description, EffortSupport: effort, EffortLevels: append([]string{}, m.Efforts...), Access: protocol.ModelCapabilitySupportUnknown})
		}
	}
	return models, nil
}

func (l *serverLink) targets(ctx context.Context, cwd string) ([]protocol.ExecutionTarget, error) {
	p, err := l.connection()
	if err != nil {
		return nil, err
	}
	c, release, err := p.borrow(ctx, l, true)
	if err != nil {
		return nil, err
	}
	defer release()
	targets := []protocol.ExecutionTarget{}
	if err := c.Call(ctx, "targets/list", map[string]any{"cwd": cwd}, &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (l *serverLink) sessions() []protocol.SessionID {
	var open []protocol.SessionID
	for _, session := range l.d.store.List("") {
		if l.d.store.SessionLink(session.ID) == l.b.kind {
			open = append(open, session.ID)
		}
	}
	return open
}

func (l *serverLink) holder(conversation string) protocol.SessionID {
	for _, sessionID := range l.d.store.OpenSessionsHolding(conversation) {
		if l.d.store.SessionLink(sessionID) == l.b.kind {
			return sessionID
		}
	}
	return ""
}

func (l *serverLink) ensureServer(ctx context.Context, opts *ptybackend.SpawnOptions) (*effectrpc.Client, error) {
	p, err := l.connection()
	if err != nil {
		return nil, err
	}
	return p.ensure(ctx, l, opts, true)
}

func (l *serverLink) startServer(ctx context.Context, opts *ptybackend.SpawnOptions, terminal harness.TerminalID) error {
	if _, live := l.d.liveTerminals(ctx)[terminal]; live {
		if err := l.d.ptyBackend.Remove(ctx, terminal); err != nil && !errors.Is(err, pty.ErrSessionNotFound) && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clear the exited %s server: %w", l.b.name, err)
		}
	}
	executable := l.executable(opts)
	home := l.home()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	loginEnv, err := l.d.launchEnv(ctx)
	if err != nil {
		return fmt.Errorf("start the %s server: %w", l.b.name, err)
	}
	p := l.b.declaration.Load()
	args, homeVariable := p.ServeArgs, p.HomeEnv
	argv, env := append([]string{executable}, args...), append(append([]string(nil), serverEnv...), homeVariable+"="+home)
	spawn := ptybackend.SpawnOptions{
		ID: terminal, Agent: string(terminal), Label: l.b.name + " server", CWD: home, Cols: 80, Rows: 24,
		Executable:    resolvedBinary(executable),
		AgentArgv:     argv,
		AgentEnv:      env,
		LoginShellEnv: loginEnv,
		DaemonEnv:     l.d.spawnRoutingEnv(),
	}
	if err := l.d.ptyBackend.Spawn(ctx, withLifecycleID(spawn)); err != nil {
		return fmt.Errorf("start the %s server: %w", l.b.name, err)
	}
	l.logf("started %s for %s in terminal %s", strings.Join(argv, " "), home, terminal)
	return nil
}

func (l *serverLink) tracksCompletions() bool {
	if p := l.b.declaration.Load(); p != nil {
		return p.Recovery == "numbered-completions"
	}
	return false
}

func (l *serverLink) launchStarted(id protocol.SessionID) {
	p, err := l.connection()
	if err == nil {
		p.reconcile(l, []protocol.SessionID{id})
	}
}

func (l *serverLink) remembersItsLastRun(sessionID protocol.SessionID) bool {
	session := l.d.store.Get(sessionID)
	return session != nil && (session.State == protocol.SessionStateIdle || session.State == protocol.SessionStateWaitingInput)
}

func (l *serverLink) settlesOnServer(sessionID protocol.SessionID) bool {
	session := l.d.store.Get(sessionID)
	return session != nil && (session.State == protocol.SessionStateWorking || session.State == protocol.SessionStatePendingApproval)
}

func (l *serverLink) lastRuns(c *effectrpc.Client) map[string]*serverEnded {
	var conversations []struct {
		ID    string       `json:"id"`
		Ended *serverEnded `json:"ended"`
	}
	ctx, cancel := context.WithTimeout(l.d.life.Context(), serverCallLimit)
	defer cancel()
	if err := c.Call(ctx, "conversation/list", nil, &conversations); err != nil {
		l.logf("reading how runs ended while attn was away: %v", err)
	}
	ended := make(map[string]*serverEnded, len(conversations))
	for _, conversation := range conversations {
		ended[conversation.ID] = conversation.Ended
	}
	return ended
}

func (l *serverLink) unseen(sessionID protocol.SessionID, ended *serverEnded) *serverEnded {
	if ended == nil || ended.Completion == 0 || ended.Completion > l.d.sessionMonitor(sessionID).ObservedServerCompletion() {
		return ended
	}
	return nil
}

func (l *serverLink) observe(sessionID protocol.SessionID, status string, ended *serverEnded) {
	monitor := l.d.sessionMonitor(sessionID)
	if monitor == nil {
		return
	}
	now := time.Now()
	l.d.traceStateEvidence(sessionID, sessionmonitor.Origin{Source: string(sessionstate.SourceAppServer), ObservedAt: now}, status)
	evidence, _ := monitor.Evidence()
	switch status {
	case "working":
		if !evidence.TurnOpen {
			monitor.RecordBracket(protocol.StateWorking)
		}
		monitor.RecordServerStatus(pty.ProgramWorking, time.Now())
	case "waiting":
		monitor.RecordServerStatus(pty.ProgramBlockedPermission, now)
	case "idle":
		answered := ended != nil && ended.Outcome == "answered"
		if evidence.TurnOpen || evidence.ToolOpen || ended != nil {
			monitor.RecordTurnEnded(answered)
		}
		monitor.RecordServerStatus(pty.ProgramIdle, now)
		switch {
		case ended == nil:
		case answered:
			said := ended.Said
			monitor.RecordServerCompletion(ended.Completion)
			l.d.life.Go(l.b.agent+"Classify", func() { monitor.ClassifyMessage(said) })
		case ended.Outcome == "interrupted":
			monitor.RecordTurnAborted("the "+l.b.name+" run was interrupted", time.Now(), time.Now())
			monitor.RecordServerCompletion(ended.Completion)
		default:
			monitor.RecordStopFailure(ended.Outcome, ended.Message)
			monitor.RecordServerCompletion(ended.Completion)
		}
	}
}

func (l *serverLink) Voices() []harness.Voice {
	return []harness.Voice{harness.VoiceUser, harness.VoiceAttn}
}

type serverSessionLink struct {
	link    *serverLink
	session protocol.SessionID
}

func (s serverSessionLink) Voices() []harness.Voice { return s.link.Voices() }

func (s serverSessionLink) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	in.Session = s.session
	return s.link.Deliver(ctx, in)
}

func (l *serverLink) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	conversation := l.d.linkConversation(in.Session)
	if conversation == "" {
		return harness.Custody{Reason: fmt.Sprintf("session %s holds no %s conversation", in.Session, l.b.name)}
	}
	author := "attn"
	if in.Voice == harness.VoiceUser {
		author = userName()
	}
	c, err := l.ensureServer(ctx, nil)
	if err == nil {
		callCtx, cancel := context.WithTimeout(ctx, serverCallLimit)
		err = c.Call(callCtx, "conversation/submit", map[string]any{"conversation": conversation, "requestId": in.ID, "author": author, "text": in.Text}, nil)
		cancel()
	}
	if err != nil {
		return harness.Custody{Reason: fmt.Sprintf("deliver to %s conversation %s: %v", l.b.name, conversation, err)}
	}
	return harness.Custody{Taken: true, At: time.Now()}
}

func userName() string {
	if name := strings.TrimSpace(os.Getenv("USER")); name != "" {
		return name
	}
	return "you"
}

func (l *serverLink) sessionClosed(sessionID protocol.SessionID, _ string, conversation string) {
	declaration := l.b.declaration.Load()
	if declaration == nil || declaration.OnClose == "leave-running" || conversation == "" || l.d.store.OtherOpenSessionHolding(conversation, sessionID) != "" {
		return
	}
	ctx, cancel := context.WithTimeout(l.d.life.Context(), serverCallLimit)
	defer cancel()
	p, err := l.connection()
	if err != nil {
		return
	}
	c, release, err := p.borrow(ctx, l, false)
	if err != nil {
		return
	}
	defer release()
	if err := c.Call(ctx, "conversation/interrupt", map[string]any{"conversation": conversation}, nil); err != nil {
		l.logf("interrupting conversation %s of closed session %s: %v", conversation, sessionID, err)
	}
}

func (l *serverLink) profileReleased(string) { l.releaseUnlessUsed() }

func (l *serverLink) releaseUnlessUsed() {
	if p, err := l.connection(); err == nil {
		p.releaseUnlessUsed()
	}
}

func (l *serverLink) terminalExited(t harness.TerminalID) {
	if t != l.b.terminal {
		l.terminalDropped(t)
		return
	}
	l.logf("the %s server exited", l.b.name)
	if err := l.d.removePTYSession(t); err != nil {
		l.d.logf("pty backend remove on exit failed for %s: %v", t, err)
	}
}

func (l *serverLink) recoverProcesses(ctx context.Context) {
	if len(l.sessions()) == 0 {
		return
	}
	l.d.life.Go(l.b.agent+"Reconnect", func() {
		if _, err := l.ensureServer(ctx, nil); err != nil {
			l.logf("reconnect to the %s server: %v", l.b.name, err)
			return
		}
		if p, err := l.connection(); err == nil {
			p.reconcile(l, l.sessions())
		}
	})
}

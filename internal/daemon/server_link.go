package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
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
	d        *Daemon
	b        *serverBinding
	ensureMu sync.Mutex
	mu       sync.Mutex
	control  *effectrpc.Client
	ended    map[string]serverEnded
	busy     map[string]string
	events   linkEvents
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

func newServerLink(d *Daemon, b *serverBinding) *serverLink {
	l := &serverLink{d: d, b: b, ended: make(map[string]serverEnded)}
	d.life.Go(b.agent+"Stop", func() {
		<-d.life.Done()
		if c := l.client(); c != nil {
			c.Close()
		}
	})
	return l
}

func (l *serverLink) logf(format string, args ...any) {
	l.d.logf(l.b.agent+": "+format, args...)
}

func (l *serverLink) kind() string { return l.b.kind }

func (l *serverLink) canRun(req *spawnRequest) bool {
	return req.agent == l.b.agent && (!req.hasPluginDriver || l.b.plugin == req.pluginDriver.PluginName) && !req.isShell && req.policy.unattendedLaunch.Executable == ""
}

func (l *serverLink) enabledForNewLaunches() bool {
	if l.b.plugin != "" {
		reg, conn := l.d.ensurePluginRegistry().driverConnection(l.b.agent, l.b.plugin)
		return l.b.declaration.Load() != nil && conn != nil && reg.Capabilities["server_binding"]
	}
	return l.d.on(l.b.feature)
}

func (l *serverLink) admit(profile, conversation string) error {
	holder := l.d.store.OpenSessionHoldingOutside(profile, conversation)
	if holder == "" || l.d.store.SessionLink(holder) != l.b.kind {
		return nil
	}
	if session := l.d.store.Get(holder); session != nil {
		return fmt.Errorf("conversation %s is open in a session of profile %s; close it there first", conversation, session.ProfileID)
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
	c, err := l.ensureServer(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer l.releaseUnlessUsed()
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
	c, err := l.ensureServer(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer l.releaseUnlessUsed()
	targets := []protocol.ExecutionTarget{}
	if err := c.Call(ctx, "targets/list", map[string]any{"cwd": cwd}, &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (l *serverLink) client() *effectrpc.Client {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.control
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
	sessionID := l.d.store.OtherOpenSessionHolding(conversation, "")
	if sessionID == "" || l.d.store.SessionLink(sessionID) != l.b.kind {
		return ""
	}
	return sessionID
}

func (l *serverLink) ensureServer(ctx context.Context, opts *ptybackend.SpawnOptions) (*effectrpc.Client, error) {
	l.ensureMu.Lock()
	defer l.ensureMu.Unlock()
	if c := l.client(); c != nil {
		return c, nil
	}
	socket := l.socketPath()
	deadline := time.Now().Add(serverStartLimit)
	started := false
	for {
		attempt, cancel := context.WithTimeout(ctx, serverCallLimit)
		c, err := l.connect(attempt, socket, "attn", buildinfo.Version)
		cancel()
		if err == nil {
			l.hold(c)
			return c, nil
		}
		var refused *effectrpc.Error
		if errors.As(err, &refused) || ctx.Err() != nil {
			return nil, err
		}
		if !started {
			if err := l.startServer(ctx, opts); err != nil {
				return nil, err
			}
			started = true
		} else if time.Now().After(deadline) {
			return nil, fmt.Errorf("the %s server at %s did not answer within %s: %w", l.b.name, socket, serverStartLimit, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (l *serverLink) startServer(ctx context.Context, opts *ptybackend.SpawnOptions) error {
	terminal := l.b.terminal
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
	args, homeVariable := []string{"serve"}, l.b.homeVariable
	if p := l.b.declaration.Load(); p != nil {
		args, homeVariable = p.ServeArgs, p.HomeEnv
	}
	argv, env := append([]string{executable}, args...), append(append([]string(nil), serverEnv...), homeVariable+"="+home)
	spawn := ptybackend.SpawnOptions{
		ID: terminal, Agent: l.b.kind, Label: l.b.name + " server", CWD: home, Cols: 80, Rows: 24,
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

func (l *serverLink) hold(c *effectrpc.Client) {
	l.mu.Lock()
	l.control = c
	l.mu.Unlock()
	if !l.d.life.Go(l.b.agent+"Status", func() { l.watch(c) }) {
		c.Close()
	}
}

func (l *serverLink) watch(c *effectrpc.Client) {
	err := c.Stream(l.d.life.Context(), "status/watch", l.statusWatchParams(), func(item json.RawMessage) {
		var tree serverTree
		if json.Unmarshal(item, &tree) == nil {
			l.events.run(l.d, func() { l.apply(c, tree) })
		}
	})
	c.Close()
	l.mu.Lock()
	current := l.control == c
	if current {
		l.control = nil
	}
	l.mu.Unlock()
	if !current || l.d.life.Context().Err() != nil {
		return
	}
	l.logf("lost the status of the %s server: %v", l.b.name, err)
	if len(l.sessions()) == 0 {
		return
	}
	if _, err := l.ensureServer(l.d.life.Context(), nil); err != nil {
		l.logf("reconnecting to the %s server: %v", l.b.name, err)
	}
}

func (l *serverLink) statusWatchParams() any {
	numbered := l.b.tracksCompletions
	if p := l.b.declaration.Load(); p != nil {
		numbered = p.Recovery == "numbered-completions"
	}
	if !numbered {
		return nil
	}
	after := map[string]int64{}
	for _, sessionID := range l.sessions() {
		if conversation := l.d.linkConversation(sessionID); conversation != "" {
			after[conversation] = l.d.store.SessionRuntimeRecords(sessionID).ServerCompletion
		}
	}
	return map[string]any{"after": after}
}

func (l *serverLink) apply(c *effectrpc.Client, tree serverTree) {
	if tree.Tag == "trees" {
		l.busy = make(map[string]string, len(tree.Trees))
		for _, t := range tree.Trees {
			l.busy[t.Conversation] = t.Status
		}
		var lastRuns map[string]*serverEnded
		for _, sessionID := range l.sessions() {
			conversation := l.d.linkConversation(sessionID)
			if status, ok := l.busy[conversation]; ok {
				l.observe(sessionID, status, nil)
				continue
			}
			if !l.settlesOnServer(sessionID) {
				continue
			}
			if lastRuns == nil {
				lastRuns = l.lastRuns(c)
			}
			l.observe(sessionID, "idle", l.unseen(sessionID, lastRuns[conversation]))
		}
		return
	}
	if tree.Ended != nil && tree.Status != "idle" {
		l.ended[tree.Conversation] = *tree.Ended
		return
	}
	ended := tree.Ended
	if remembered, ok := l.ended[tree.Conversation]; ok && tree.Status == "idle" {
		if ended == nil {
			ended = &remembered
		}
		delete(l.ended, tree.Conversation)
	}
	if tree.Status == "idle" {
		delete(l.busy, tree.Conversation)
	} else {
		if l.busy == nil {
			l.busy = make(map[string]string)
		}
		l.busy[tree.Conversation] = tree.Status
	}
	holder := l.holder(tree.Conversation)
	ended = l.unseen(holder, ended)
	if ended != nil && tree.Status == "idle" {
		l.d.sessionMonitor(holder).RecordRunAnswer(ended.Said)
	}
	l.observe(holder, tree.Status, ended)
}

func (l *serverLink) launchStarted(sessionID protocol.SessionID) {
	l.events.run(l.d, func() {
		conversation := l.d.linkConversation(sessionID)
		if status, busy := l.busy[conversation]; busy {
			l.observe(sessionID, status, nil)
			return
		}
		if l.remembersItsLastRun(sessionID) {
			return
		}
		var ended *serverEnded
		if c := l.client(); c != nil {
			ended = l.unseen(sessionID, l.lastRuns(c)[conversation])
		}
		l.observe(sessionID, "idle", ended)
	})
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
	leavesRun := l.b.closeLeavesRun
	if p := l.b.declaration.Load(); p != nil {
		leavesRun = p.OnClose == "leave-running"
	}
	if leavesRun || conversation == "" || l.d.store.OtherOpenSessionHolding(conversation, sessionID) != "" {
		return
	}
	ctx, cancel := context.WithTimeout(l.d.life.Context(), serverCallLimit)
	defer cancel()
	c, err := l.connect(ctx, l.socketPath(), "attn", buildinfo.Version)
	if err != nil {
		return
	}
	defer c.Close()
	if err := c.Call(ctx, "conversation/interrupt", map[string]any{"conversation": conversation}, nil); err != nil {
		l.logf("interrupting conversation %s of closed session %s: %v", conversation, sessionID, err)
	}
}

func (l *serverLink) profileReleased(string) { l.releaseUnlessUsed() }

func (l *serverLink) releaseUnlessUsed() {
	l.ensureMu.Lock()
	defer l.ensureMu.Unlock()
	if len(l.sessions()) > 0 {
		return
	}
	l.mu.Lock()
	c := l.control
	l.control = nil
	l.mu.Unlock()
	if c != nil {
		c.Close()
		l.logf("let go of the %s server; no session uses it", l.b.name)
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
		}
	})
}

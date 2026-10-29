package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/nisse"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
)

const (
	nisseLinkKind                            = "nisse-server"
	nisseAgent                               = "nisse"
	nisseServerTerminal   harness.TerminalID = "nisse-server"
	nisseServerStartLimit                    = 30 * time.Second
	nisseCallLimit                           = 5 * time.Second
)

var nisseServerEnv = []string{"ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_AGENT=", "BUN_GC_TIMER_DISABLE=1"}

type nisseLink struct {
	d        *Daemon
	ensureMu sync.Mutex
	mu       sync.Mutex
	control  *nisse.Client
	ended    map[string]nisseEnded
	events   linkEvents
}

type nisseTree struct {
	Tag          string      `json:"_tag"`
	Conversation string      `json:"conversation"`
	Status       string      `json:"status"`
	Ended        *nisseEnded `json:"ended"`
	Trees        []nisseTree `json:"trees"`
}

type nisseEnded struct {
	Outcome string `json:"outcome"`
	Message string `json:"message"`
	Said    string `json:"said"`
}

func (d *Daemon) nisse() *nisseLink {
	d.nisseOnce.Do(func() {
		l := &nisseLink{d: d, ended: make(map[string]nisseEnded)}
		d.nisseState = l
		d.life.Go("nisseStop", func() {
			<-d.life.Done()
			if c := l.client(); c != nil {
				c.Close()
			}
			if d.ptyBackendMode() == "embedded" {
				if err := d.ptyBackend.Kill(context.Background(), nisseServerTerminal, syscall.SIGKILL); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
					d.logf("nisse: killing the Nisse server: %v", err)
				}
			}
		})
	})
	return d.nisseState
}

func (d *Daemon) nisseEnabled() bool {
	return parseBooleanSetting(d.homeSetting(SettingNisseEnabled))
}

func (d *Daemon) agentOffered(agent string) bool {
	return agent != nisseAgent || d.nisseEnabled()
}

func (l *nisseLink) home() string {
	env := pty.MergeEnvironment(os.Environ(), l.d.loginShellEnvironment())
	lookup := func(key string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if name, value, ok := strings.Cut(env[i], "="); ok && name == key {
				return value
			}
		}
		return ""
	}
	if home := strings.TrimSpace(lookup("NISSE_HOME")); home != "" {
		return home
	}
	return filepath.Join(lookup("HOME"), ".nisse")
}

func (l *nisseLink) kind() string { return nisseLinkKind }

func (l *nisseLink) canRun(req *spawnRequest) bool {
	return req.agent == nisseAgent && !req.hasPluginDriver && !req.isShell && req.policy.unattendedLaunch.Executable == ""
}

func (l *nisseLink) enabledForNewLaunches() bool { return l.d.nisseEnabled() }

func (l *nisseLink) admit(profile, conversation string) error {
	holder := l.d.store.OpenSessionHoldingOutside(profile, conversation)
	if holder == "" || l.d.store.SessionLink(holder) != nisseLinkKind {
		return nil
	}
	if session := l.d.store.Get(holder); session != nil {
		return fmt.Errorf("conversation %s is open in a session of profile %s; close it there first", conversation, session.ProfileID)
	}
	return nil
}

func (l *nisseLink) prepareLaunch(opts *ptybackend.SpawnOptions, profile string) error {
	if _, err := l.ensureServer(l.d.life.Context(), opts); err != nil {
		return err
	}
	return l.d.store.SaveTerminalView(store.TerminalView{TerminalID: string(opts.ID), Link: nisseLinkKind, ProfileID: profile})
}

func (l *nisseLink) relaunch(opts *ptybackend.SpawnOptions) error {
	if _, err := l.ensureServer(l.d.life.Context(), opts); err != nil {
		l.d.logf("nisse: relaunching terminal %s before its server answers: %v", opts.ID, err)
	}
	return nil
}

func (l *nisseLink) resolveCaller(protocol.TerminalID) (protocol.SessionID, harness.TerminalID, bool) {
	return "", "", false
}

func (l *nisseLink) runsLink(t harness.TerminalID) bool { return t == nisseServerTerminal }

func (l *nisseLink) serverTerminal(string) harness.TerminalID { return nisseServerTerminal }

func (l *nisseLink) renamed(protocol.SessionID, string) {}

func (l *nisseLink) setAsideAtClose(string) bool { return false }

func (l *nisseLink) restore(string, string) error { return nil }

func (l *nisseLink) transcriptPath(path string) string { return path }

func (l *nisseLink) terminalDropped(t harness.TerminalID) {
	if err := l.d.store.DeleteTerminalView(string(t)); err != nil {
		l.d.logf("nisse: %v", err)
	}
}

func (l *nisseLink) recoverViews(ctx context.Context) {
	views, err := l.d.store.TerminalViews(nisseLinkKind)
	if err != nil {
		l.d.logf("nisse: %v", err)
		return
	}
	live := l.d.liveTerminals(ctx)
	for _, view := range views {
		if _, running := live[harness.TerminalID(view.TerminalID)]; !running {
			l.terminalDropped(harness.TerminalID(view.TerminalID))
		}
	}
}

func (l *nisseLink) client() *nisse.Client {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.control
}

func (l *nisseLink) sessions() []protocol.SessionID {
	var open []protocol.SessionID
	for _, session := range l.d.store.List("") {
		if l.d.store.SessionLink(session.ID) == nisseLinkKind {
			open = append(open, session.ID)
		}
	}
	return open
}

func (l *nisseLink) holder(conversation string) protocol.SessionID {
	sessionID := l.d.store.OtherOpenSessionHolding(conversation, "")
	if sessionID == "" || l.d.store.SessionLink(sessionID) != nisseLinkKind {
		return ""
	}
	return sessionID
}

func (l *nisseLink) executable(opts *ptybackend.SpawnOptions) string {
	driver := agentdriver.MustGet(nisseAgent)
	if opts != nil {
		if configured := configuredLaunchExecutable(driver, opts); configured != "" {
			return driver.ResolveExecutable(configured)
		}
	}
	return driver.ResolveExecutable(l.d.machineSetting(MachineSetting(executableSettingKey(nisseAgent))))
}

func (l *nisseLink) ensureServer(ctx context.Context, opts *ptybackend.SpawnOptions) (*nisse.Client, error) {
	l.ensureMu.Lock()
	defer l.ensureMu.Unlock()
	if c := l.client(); c != nil {
		return c, nil
	}
	socket := nisse.Socket(l.home())
	deadline := time.Now().Add(nisseServerStartLimit)
	started := false
	for {
		attempt, cancel := context.WithTimeout(ctx, nisseCallLimit)
		c, err := nisse.Connect(attempt, socket, "attn", buildinfo.Version)
		cancel()
		if err == nil {
			l.hold(c)
			return c, nil
		}
		var refused *nisse.Error
		if errors.As(err, &refused) || ctx.Err() != nil {
			return nil, err
		}
		if !started {
			if err := l.startServer(ctx, opts); err != nil {
				return nil, err
			}
			started = true
		} else if time.Now().After(deadline) {
			return nil, fmt.Errorf("the Nisse server at %s did not answer within %s: %w", socket, nisseServerStartLimit, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (l *nisseLink) startServer(ctx context.Context, opts *ptybackend.SpawnOptions) error {
	if _, live := l.d.liveTerminals(ctx)[nisseServerTerminal]; live {
		if err := l.d.ptyBackend.Remove(ctx, nisseServerTerminal); err != nil && !errors.Is(err, pty.ErrSessionNotFound) && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clear the exited Nisse server: %w", err)
		}
	}
	executable := l.executable(opts)
	home := l.home()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	spawn := ptybackend.SpawnOptions{
		ID: nisseServerTerminal, Agent: nisseLinkKind, Label: "Nisse server", CWD: home, Cols: 80, Rows: 24,
		Executable:    resolvedBinary(executable),
		AgentArgv:     []string{executable, "serve"},
		AgentEnv:      append(append([]string(nil), nisseServerEnv...), "NISSE_HOME="+home),
		LoginShellEnv: l.d.loginShellEnvironment(),
		DaemonEnv:     l.d.spawnRoutingEnv(),
	}
	if err := l.d.ptyBackend.Spawn(ctx, withLifecycleID(spawn)); err != nil {
		return fmt.Errorf("start the Nisse server: %w", err)
	}
	l.d.logf("nisse: started %s serve for %s in terminal %s", executable, home, nisseServerTerminal)
	return nil
}

func (l *nisseLink) hold(c *nisse.Client) {
	l.mu.Lock()
	l.control = c
	l.mu.Unlock()
	if !l.d.life.Go("nisseStatus", func() { l.watch(c) }) {
		c.Close()
	}
}

func (l *nisseLink) watch(c *nisse.Client) {
	err := c.Stream(l.d.life.Context(), "status/watch", nil, func(item json.RawMessage) {
		var tree nisseTree
		if json.Unmarshal(item, &tree) == nil {
			l.events.run(l.d, func() { l.apply(tree) })
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
	l.d.logf("nisse: lost the status of the Nisse server: %v", err)
	if len(l.sessions()) == 0 {
		return
	}
	if _, err := l.ensureServer(l.d.life.Context(), nil); err != nil {
		l.d.logf("nisse: reconnecting to the Nisse server: %v", err)
	}
}

func (l *nisseLink) apply(tree nisseTree) {
	if tree.Tag == "trees" {
		busy := make(map[string]nisseTree, len(tree.Trees))
		for _, t := range tree.Trees {
			busy[t.Conversation] = t
		}
		for _, sessionID := range l.sessions() {
			t, ok := busy[l.d.linkConversation(sessionID)]
			if !ok {
				t.Status = "idle"
			}
			l.observe(sessionID, t.Status, nil)
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
	l.observe(l.holder(tree.Conversation), tree.Status, ended)
}

func (l *nisseLink) observe(sessionID protocol.SessionID, status string, ended *nisseEnded) {
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
			l.d.life.Go("nisseClassify", func() { monitor.ClassifyMessage(said) })
		case ended.Outcome == "interrupted":
			monitor.RecordTurnAborted("the Nisse run was interrupted", time.Now(), time.Now())
		default:
			monitor.RecordStopFailure(ended.Outcome, ended.Message)
		}
	}
}

func (l *nisseLink) Voices() []harness.Voice {
	return []harness.Voice{harness.VoiceUser, harness.VoiceAttn}
}

type nisseSessionLink struct {
	link    *nisseLink
	session protocol.SessionID
}

func (s nisseSessionLink) Voices() []harness.Voice { return s.link.Voices() }

func (s nisseSessionLink) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	in.Session = s.session
	return s.link.Deliver(ctx, in)
}

func (l *nisseLink) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	conversation := l.d.linkConversation(in.Session)
	if conversation == "" {
		return harness.Custody{Reason: fmt.Sprintf("session %s holds no Nisse conversation", in.Session)}
	}
	author := "attn"
	if in.Voice == harness.VoiceUser {
		author = userName()
	}
	c, err := l.ensureServer(ctx, nil)
	if err == nil {
		callCtx, cancel := context.WithTimeout(ctx, nisseCallLimit)
		err = c.Call(callCtx, "conversation/submit", map[string]any{"conversation": conversation, "requestId": in.ID, "author": author, "text": in.Text}, nil)
		cancel()
	}
	if err != nil {
		return harness.Custody{Reason: fmt.Sprintf("deliver to Nisse conversation %s: %v", conversation, err)}
	}
	return harness.Custody{Taken: true, At: time.Now()}
}

func userName() string {
	if name := strings.TrimSpace(os.Getenv("USER")); name != "" {
		return name
	}
	return "you"
}

func (l *nisseLink) sessionClosed(sessionID protocol.SessionID, _ string, conversation string) {
	if conversation == "" || l.d.store.OtherOpenSessionHolding(conversation, sessionID) != "" {
		return
	}
	ctx, cancel := context.WithTimeout(l.d.life.Context(), nisseCallLimit)
	defer cancel()
	c, err := nisse.Connect(ctx, nisse.Socket(l.home()), "attn", buildinfo.Version)
	if err != nil {
		return
	}
	defer c.Close()
	if err := c.Call(ctx, "conversation/interrupt", map[string]any{"conversation": conversation}, nil); err != nil {
		l.d.logf("nisse: interrupting conversation %s of closed session %s: %v", conversation, sessionID, err)
	}
}

func (l *nisseLink) profileReleased(string) {
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
		l.d.logf("nisse: let go of the Nisse server; no session uses it")
	}
}

func (l *nisseLink) terminalExited(t harness.TerminalID) {
	if t != nisseServerTerminal {
		l.terminalDropped(t)
		return
	}
	l.d.logf("nisse: the Nisse server exited")
	if err := l.d.removePTYSession(t); err != nil {
		l.d.logf("pty backend remove on exit failed for %s: %v", t, err)
	}
}

func (l *nisseLink) recoverProcesses(ctx context.Context) {
	if len(l.sessions()) == 0 {
		return
	}
	if _, err := l.ensureServer(ctx, nil); err != nil {
		l.d.logf("nisse: reconnect to the Nisse server: %v", err)
	}
}

package appserver

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
)

const (
	serverStartLimit = 30 * time.Second
	serverCallLimit  = 5 * time.Second
)

type Link struct {
	c *Client
	b *serverBinding
}

type serverTree struct {
	Tag          string        `json:"_tag"`
	Conversation string        `json:"conversation"`
	Status       string        `json:"status"`
	Ended        *serverEnded  `json:"ended"`
	Trees        []serverTree  `json:"trees"`
	Author       serverAuthor  `json:"author"`
	Shown        []serverShown `json:"shown"`
}

type serverEnded struct {
	Completion int64  `json:"completion"`
	Outcome    string `json:"outcome"`
	Message    string `json:"message"`
	Said       string `json:"said"`
}

func newLink(c *Client, b *serverBinding) *Link { return &Link{c: c, b: b} }

func (l *Link) logf(format string, args ...any) {
	l.c.host.Logf(l.b.agent+": "+format, args...)
}

func (l *Link) serves(agent string) bool { return agent == l.b.agent }

func (l *Link) holds(sessionID protocol.SessionID) bool {
	agent, lifetime := l.c.host.Sessions().Lifetime(sessionID)
	return lifetime == harness.InServer && l.serves(agent)
}

func (l *Link) enabledForNewLaunches() bool {
	return l.b.declaration.Load() != nil && l.b.enabled != nil && l.b.enabled()
}

func (l *Link) admit(profile, host, conversation string) error {
	for _, holder := range l.c.host.Sessions().Holding(conversation) {
		if l.c.serverHolding(holder) == nil {
			continue
		}
		if session := l.c.host.Sessions().Get(holder); session != nil {
			if host != "" {
				if protocol.Deref(session.Host) != host {
					continue
				}
			} else if l.c.host.Sessions().Remote(holder) {
				continue
			}
			if session.ProfileID != profile {
				return fmt.Errorf("conversation %s is open in a session of profile %s; close it there first", conversation, session.ProfileID)
			}
			if !l.holds(holder) {
				return fmt.Errorf("conversation %s is owned by harness %s; close its session before using harness %s", conversation, session.Agent, l.b.agent)
			}
		}
	}
	return nil
}

func (l *Link) prepareLaunch(req *harness.LaunchRequest) error {
	if _, err := l.ensureServer(l.c.host.Life().Context(), req); err != nil {
		return err
	}
	return l.c.host.Records().SaveTerminalView(harness.TerminalView{Terminal: req.Terminal, Kind: l.b.kind, Profile: req.Profile})
}

func (l *Link) relaunch(req *harness.LaunchRequest) error {
	_, err := l.ensureServer(l.c.host.Life().Context(), req)
	var refused *serverHandshakeRefusal
	if errors.As(err, &refused) {
		return err
	}
	if err != nil {
		l.logf("relaunching terminal %s before its server answers: %v", req.Terminal, err)
	}
	return nil
}

func (l *Link) resolveCaller(t protocol.TerminalID) (protocol.SessionID, harness.TerminalID, bool) {
	conversation, ok := l.b.conversationOf(harness.TerminalID(t))
	if !ok {
		return "", "", false
	}
	session := l.holder(conversation)
	if session == "" {
		return "", "", true
	}
	return session, l.c.host.Sessions().PrimaryTerminal(session), true
}

func (l *Link) ServerTerminal(string) harness.TerminalID { return l.b.terminal }

func (l *Link) terminalDropped(t harness.TerminalID) {
	if err := l.c.host.Records().DeleteTerminalView(t); err != nil {
		l.logf("%v", err)
	}
}

func (l *Link) recoverViews(ctx context.Context) {
	views, err := l.c.host.Records().TerminalViews(l.b.kind)
	if err != nil {
		l.logf("%v", err)
		return
	}
	live := l.c.host.Terminals().Live(ctx)
	for _, view := range views {
		if _, running := live[view.Terminal]; !running {
			l.terminalDropped(view.Terminal)
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

func (l *Link) Models(ctx context.Context, refresh bool) (harness.Models, error) {
	models, err := l.models(ctx, refresh)
	if err != nil {
		return harness.Models{}, err
	}
	return harness.Models{List: models, Detail: "Models of the providers the " + l.b.name + " server has."}, nil
}

// RefreshParams asks the server to reread what it caches instead of answering from its cache.
func RefreshParams(refresh bool) map[string]any {
	if refresh {
		return map[string]any{"refresh": true}
	}
	return map[string]any{}
}

func (l *Link) Call(ctx context.Context, method string, params, answer any) error {
	p, err := l.connection()
	if err != nil {
		return err
	}
	c, release, err := p.borrow(ctx, l, true)
	if err != nil {
		return err
	}
	defer release()
	return c.Call(ctx, method, params, answer)
}

func (l *Link) models(ctx context.Context, refresh bool) ([]protocol.HarnessModel, error) {
	var providers []serverProviderModels
	if err := l.Call(ctx, "models/list", RefreshParams(refresh), &providers); err != nil {
		return nil, err
	}
	var models []protocol.HarnessModel
	for _, p := range providers {
		for _, m := range p.Models {
			effort := protocol.ModelCapabilitySupportUnsupported
			if len(m.Efforts) > 0 {
				effort = protocol.ModelCapabilitySupportSupported
			}
			id := m.ID
			if p.Provider != "" {
				id = p.Provider + "/" + m.ID
			}
			models = append(models, protocol.HarnessModel{Harness: l.b.agent, Provider: p.Provider, ID: id, Family: protocol.Ptr(m.ID), Name: m.Name, Description: m.Description, EffortSupport: effort, EffortLevels: append([]string{}, m.Efforts...), Access: protocol.ModelCapabilitySupportUnknown, OneShot: protocol.ModelCapabilitySupportUnknown})
		}
	}
	return models, nil
}

func (l *Link) Targets(ctx context.Context, cwd string) ([]protocol.ExecutionTarget, error) {
	targets := []protocol.ExecutionTarget{}
	if err := l.Call(ctx, "targets/list", map[string]any{"cwd": cwd}, &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (l *Link) sessions() []protocol.SessionID {
	var open []protocol.SessionID
	for _, session := range l.c.host.Sessions().List() {
		if l.runsHere(session.ID) {
			open = append(open, session.ID)
		}
	}
	return open
}

func (l *Link) runsHere(sessionID protocol.SessionID) bool {
	return l.holds(sessionID) && !l.c.host.Sessions().Remote(sessionID)
}

func (l *Link) holder(conversation string) protocol.SessionID {
	for _, sessionID := range l.c.host.Sessions().Holding(conversation) {
		if l.runsHere(sessionID) {
			return sessionID
		}
	}
	return ""
}

func (l *Link) ensureServer(ctx context.Context, req *harness.LaunchRequest) (*effectrpc.Client, error) {
	p, err := l.connection()
	if err != nil {
		return nil, err
	}
	return p.ensure(ctx, l, req, true)
}

func (l *Link) startServer(ctx context.Context, req *harness.LaunchRequest, terminal harness.TerminalID) error {
	if _, live := l.c.host.Terminals().Live(ctx)[terminal]; live {
		if err := l.c.host.Terminals().Remove(ctx, terminal); err != nil {
			return fmt.Errorf("clear the exited %s server: %w", l.b.name, err)
		}
	}
	executable := l.executable(req)
	home := l.home()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	p := l.b.declaration.Load()
	args, homeVariable := p.ServeArgs, p.HomeEnv
	argv, env := append([]string{executable}, args...), []string{"BUN_GC_TIMER_DISABLE=1", homeVariable + "=" + home}
	spawn := harness.TerminalSpec{ID: terminal, Label: l.b.name + " server", Dir: home, Executable: resolvedBinary(executable), Argv: argv, Env: env}
	if err := l.c.host.Terminals().Start(ctx, spawn); err != nil {
		return fmt.Errorf("start the %s server: %w", l.b.name, err)
	}
	l.logf("started %s for %s in terminal %s", strings.Join(argv, " "), home, terminal)
	return nil
}

func (l *Link) tracksCompletions() bool {
	if p := l.b.declaration.Load(); p != nil {
		return p.Recovery == "numbered-completions"
	}
	return false
}

func (l *Link) launchStarted(id protocol.SessionID) {
	p, err := l.connection()
	if err == nil {
		p.reconcile(l, []protocol.SessionID{id}, true)
	}
}

func (l *Link) remembersItsLastRun(sessionID protocol.SessionID) bool {
	session := l.c.host.Sessions().Get(sessionID)
	return session != nil && (session.State == protocol.SessionStateIdle || session.State == protocol.SessionStateWaitingInput)
}

func (l *Link) settlesOnServer(sessionID protocol.SessionID) bool {
	session := l.c.host.Sessions().Get(sessionID)
	return session != nil && (session.State == protocol.SessionStateWorking || session.State == protocol.SessionStatePendingApproval)
}

func (l *Link) lastRuns(c *effectrpc.Client) map[string]*serverEnded {
	var conversations []struct {
		ID    string       `json:"id"`
		Ended *serverEnded `json:"ended"`
	}
	ctx, cancel := context.WithTimeout(l.c.host.Life().Context(), serverCallLimit)
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

func (l *Link) unseen(sessionID protocol.SessionID, ended *serverEnded) *serverEnded {
	if ended == nil || ended.Completion == 0 || ended.Completion > l.observedCompletion(sessionID) {
		return ended
	}
	return nil
}

func (l *Link) observeQuiet(sessionID protocol.SessionID, ended *serverEnded) {
	if ended != nil {
		l.observe(sessionID, false, "idle", ended)
		return
	}
	l.observe(sessionID, true, "idle", nil)
}

func (l *Link) observedCompletion(sessionID protocol.SessionID) int64 {
	evidence, _ := l.c.host.Sessions().Evidence(sessionID)
	return evidence.ObservedCompletion
}

func (l *Link) observe(sessionID protocol.SessionID, quiet bool, status string, ended *serverEnded) {
	evidence, ok := l.c.host.Sessions().Evidence(sessionID)
	if !ok || status == "idle" && l.holdsOwnInterruptedEnd(sessionID, ended) {
		return
	}
	l.c.host.Sessions().TraceClaim(sessionID, quiet, status, time.Now())
	reporter := harness.FromSession(sessionID)
	turn := func(e harness.TurnEvent) {
		e.Inferred = quiet
		l.c.host.Events().Turn(reporter, time.Now(), e)
	}
	switch status {
	case "working":
		if !evidence.TurnOpen {
			turn(harness.TurnEvent{Turn: harness.TurnRunning})
		}
		turn(harness.TurnEvent{Turn: harness.TurnRunning, Status: true})
	case "waiting":
		turn(harness.TurnEvent{Turn: harness.TurnApproval, Status: true})
	case "idle":
		answered := ended != nil && ended.Outcome == "answered"
		if evidence.TurnOpen || evidence.ToolOpen || ended != nil {
			stop := harness.TurnEvent{Turn: harness.TurnEnded, Stop: &harness.Stop{Classifies: answered}}
			if ended != nil {
				stop.Said = &ended.Said
			}
			turn(stop)
		}
		switch {
		case ended == nil:
			turn(harness.TurnEvent{Turn: harness.TurnEnded, Status: true})
		case answered:
			turn(harness.TurnEvent{Turn: harness.TurnEnded, Status: true, Completion: ended.Completion, Said: &ended.Said})
		case ended.Outcome == "interrupted":
			turn(harness.TurnEvent{Turn: harness.TurnEnded, Status: true})
			turn(harness.TurnEvent{Turn: harness.TurnAborted, Detail: "the " + l.b.name + " run was interrupted", Completion: ended.Completion})
		default:
			turn(harness.TurnEvent{Turn: harness.TurnEnded, Status: true})
			turn(harness.TurnEvent{Turn: harness.TurnFailed, Detail: stopFailureDetail(ended.Outcome, ended.Message), Completion: ended.Completion})
		}
	default:
		turn(harness.TurnEvent{Turn: harness.TurnUnknown})
	}
}

type serverConversation struct {
	link    *Link
	session protocol.SessionID
}

func (l *Link) Conversation(facts harness.SessionFacts) harness.Conversation {
	return serverConversation{link: l, session: facts.Session}
}

func (s serverConversation) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	if refusal := s.link.turnRefusal(in.Turn); refusal != "" {
		return harness.Custody{Outcome: harness.Rejected, Reason: refusal}
	}
	if in.PendingApproval && (!s.link.Can(AbilityQuestionSafe) || in.Turn == harness.Interrupt) {
		return harness.Custody{Outcome: harness.NotMine}
	}
	return s.link.deliver(ctx, s.session, in)
}

func (s serverConversation) Compact(ctx context.Context, in harness.Input) harness.Custody {
	if !s.link.Can(AbilityCompact) {
		return harness.Custody{Outcome: harness.NotMine}
	}
	return s.compact(ctx, in)
}

func (s serverConversation) CanCompact() error {
	if s.link.Can(AbilityCompact) {
		return nil
	}
	return harness.NoCompaction(s.link.b.agent)
}

func (l *Link) deliver(ctx context.Context, session protocol.SessionID, in harness.Input) harness.Custody {
	conversation := l.c.host.Sessions().Conversation(session)
	if conversation == "" {
		return harness.Custody{Reason: fmt.Sprintf("session %s holds no %s conversation", session, l.b.name)}
	}
	params := map[string]any{"conversation": conversation, "requestId": in.ID, "author": l.inputAuthor(session, in.Voice), "text": in.Text}
	took := harness.Queue
	if l.settlesOnServer(session) {
		took = in.Turn
	}
	if l.Can(AbilitySteer) && in.Turn != harness.Interrupt {
		params["steer"] = true
	}
	c, err := l.ensureServer(ctx, nil)
	if err != nil {
		return l.undelivered(conversation, err)
	}
	call := func(method string, params, answer any) error {
		callCtx, cancel := context.WithTimeout(ctx, serverCallLimit)
		defer cancel()
		return c.Call(callCtx, method, params, answer)
	}
	if in.Turn != harness.Interrupt {
		if err := call("conversation/submit", params, nil); err != nil {
			return l.undelivered(conversation, err)
		}
		return harness.Custody{Outcome: harness.Taken, At: time.Now(), TookAs: took}
	}
	l.b.interrupting.Store(session, &ownInterrupt{})
	var interrupted serverInterrupted
	if err := call("conversation/interrupt", map[string]any{"conversation": conversation, "author": l.author(harness.VoiceAttn)}, &interrupted); err != nil {
		l.settleOwnInterrupt(session, false)
		return l.undelivered(conversation, err)
	}
	err = call("conversation/submit", params, nil)
	l.settleOwnInterrupt(session, err == nil)
	for _, withdrawn := range interrupted.Withdrawn {
		again := map[string]any{"conversation": conversation, "requestId": in.ID + ":" + withdrawn.RequestID, "author": withdrawn.Author, "text": withdrawn.Text}
		if err := call("conversation/submit", again, nil); err != nil {
			l.logf("submitting again input %s that the interrupt of conversation %s withdrew: %v", withdrawn.RequestID, conversation, err)
		}
	}
	if err != nil {
		return harness.Custody{Outcome: harness.Rejected, Reason: fmt.Sprintf("%s stopped its run but did not take the message: %v", l.b.name, err)}
	}
	return harness.Custody{Outcome: harness.Taken, At: time.Now(), TookAs: took}
}

func (l *Link) undelivered(conversation string, err error) harness.Custody {
	return harness.Custody{Reason: fmt.Sprintf("deliver to %s conversation %s: %v", l.b.name, conversation, err)}
}

type serverInterrupted struct {
	Withdrawn []struct {
		RequestID string          `json:"requestId"`
		Author    serverAuthorRef `json:"author"`
		Text      string          `json:"text"`
	} `json:"withdrawn"`
}

// ownInterrupt is an interrupt attn sent ahead of a message: the run it ended, once seen, and whether the message started the next run.
type ownInterrupt struct {
	ended    *serverEnded
	replaced bool
}

type serverAuthorRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (l *Link) author(voice harness.Voice) serverAuthorRef {
	name := "attn"
	if voice == harness.VoiceUser {
		name = userName()
	}
	return serverAuthorRef{ID: name, Name: name}
}

func (l *Link) inputAuthor(session protocol.SessionID, voice harness.Voice) serverAuthorRef {
	author := l.author(voice)
	authors := l.b.authors.Load()
	if voice != harness.VoiceUser || authors == nil {
		return author
	}
	if terminal := l.c.host.Sessions().PrimaryTerminal(session); terminal != "" {
		author.ID = authors.Of(terminal)
	}
	return author
}

func (l *Link) holdsOwnInterruptedEnd(sessionID protocol.SessionID, ended *serverEnded) bool {
	held, ok := l.b.interrupting.Load(sessionID)
	if !ok {
		return false
	}
	own := held.(*ownInterrupt)
	interrupted := ended != nil && ended.Outcome == "interrupted"
	if interrupted && !own.replaced {
		own.ended = ended
		return true
	}
	l.b.interrupting.Delete(sessionID)
	return interrupted
}

func (l *Link) settleOwnInterrupt(sessionID protocol.SessionID, replaced bool) {
	p, err := l.connection()
	if err != nil {
		l.b.interrupting.Delete(sessionID)
		return
	}
	p.events.run(l.c, func() {
		held, ok := l.b.interrupting.Load(sessionID)
		if !ok {
			return
		}
		own := held.(*ownInterrupt)
		if replaced && own.ended == nil {
			own.replaced = true
			return
		}
		l.b.interrupting.Delete(sessionID)
		if !replaced && own.ended != nil {
			l.observe(sessionID, false, "idle", own.ended)
		}
	})
}

func (l *Link) turnRefusal(turn harness.TurnUse) string {
	switch {
	case turn == harness.Steer && !l.Can(AbilitySteer):
		return l.b.name + " cannot take a steer into its running turn"
	case turn == harness.Interrupt && !l.Can(AbilityInterrupt):
		return l.b.name + " cannot interrupt its running turn"
	}
	return ""
}

func (l *Link) Can(ability Ability) bool {
	return l.b.declaration.Load().Can(ability)
}

func userName() string {
	if name := strings.TrimSpace(os.Getenv("USER")); name != "" {
		return name
	}
	return "you"
}

func (l *Link) SessionClosed(sessionID protocol.SessionID, _ string, released string) {
	declaration := l.b.declaration.Load()
	interrupt := declaration != nil && declaration.OnClose != "leave-running" && released != ""
	p, connErr := l.connection()
	if connErr == nil {
		p.events.run(l.c, func() { p.closeFeed(sessionID) })
	}
	if interrupt && connErr == nil {
		ctx, cancel := context.WithTimeout(l.c.host.Life().Context(), serverCallLimit)
		if c, release, err := p.borrow(ctx, l, false); err == nil {
			params := map[string]any{"conversation": released, "author": l.author(harness.VoiceAttn)}
			if err := c.Call(ctx, "conversation/interrupt", params, nil); err != nil {
				l.logf("interrupting conversation %s of closed session %s: %v", released, sessionID, err)
			}
			release()
		}
		cancel()
	}
	if connErr == nil && released != "" {
		cleared := make(chan struct{})
		p.events.run(l.c, func() {
			if interrupt {
				delete(p.busy, released)
				delete(p.ended, released)
			}
			p.markDeclinedIdle(released)
			close(cleared)
		})
		select {
		case <-cleared:
		case <-l.c.host.Life().Context().Done():
		}
		p.drainAdoption(l.c.host.Life().Context(), released)
	}
}

func (l *Link) releaseUnlessUsed() {
	if p, err := l.connection(); err == nil {
		p.releaseUnlessUsed()
	}
}

func (l *Link) terminalExited(t harness.TerminalID) {
	if t != l.b.terminal {
		l.terminalDropped(t)
		return
	}
	l.logf("the %s server exited", l.b.name)
	if err := l.c.host.Terminals().Remove(l.c.host.Life().Context(), t); err != nil {
		l.c.host.Logf("pty backend remove on exit failed for %s: %v", t, err)
	}
}

func (l *Link) recoverProcesses(ctx context.Context) {
	if len(l.sessions()) == 0 {
		return
	}
	l.c.host.Life().Go(l.b.agent+"Reconnect", func() {
		if _, err := l.ensureServer(ctx, nil); err != nil {
			l.logf("reconnect to the %s server: %v", l.b.name, err)
			return
		}
		if p, err := l.connection(); err == nil {
			p.reconcile(l, l.sessions(), false)
		}
	})
}

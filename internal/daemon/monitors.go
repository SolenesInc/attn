package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

type monitorBinding struct {
	entry      *sessionmonitor.Entry
	applied    sessionmonitor.View
	catchUp    *time.Timer
	retryDelay time.Duration
}

type monitorIndex struct {
	policyMu sync.Mutex
	applyMu  sync.Mutex
	mu       sync.Mutex
	sessions map[protocol.SessionID]*monitorBinding
	registry *sessionmonitor.Registry
}

func (d *Daemon) monitors() *monitorIndex {
	d.monitorsOnce.Do(func() {
		d.monitorState = &monitorIndex{sessions: make(map[protocol.SessionID]*monitorBinding), registry: sessionmonitor.NewRegistry(monitorHost{daemon: d})}
	})
	return d.monitorState
}

func (r *monitorIndex) entry(sessionID protocol.SessionID) *sessionmonitor.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if binding := r.sessions[sessionID]; binding != nil {
		return binding.entry
	}
	return nil
}

func (d *Daemon) monitorEntry(sessionID protocol.SessionID) *sessionmonitor.Entry {
	return d.monitors().entry(sessionID)
}

func (r *monitorIndex) each() map[protocol.SessionID]*sessionmonitor.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make(map[protocol.SessionID]*sessionmonitor.Entry, len(r.sessions))
	for id, binding := range r.sessions {
		entries[id] = binding.entry
	}
	return entries
}

func (d *Daemon) startSessionMonitor(sessionID protocol.SessionID) *sessionmonitor.Entry {
	return d.startSessionMonitorFrom(sessionID, "", nil)
}

func (d *Daemon) startSessionMonitorFrom(sessionID, previousID protocol.SessionID, candidate *sessionmonitor.Entry) *sessionmonitor.Entry {
	terminal := d.primaryTerminal(sessionID)
	conversation := d.store.GetSessionConversation(sessionID).NativeID
	linked := d.store.SessionLink(sessionID) == codexLinkKind
	if linked {
		if session := d.store.Get(sessionID); session != nil {
			terminal = codexServerTerminal(session.ProfileID)
		}
	}
	if terminal == "" {
		return nil
	}
	var previous *sessionmonitor.Entry
	if previousID != "" {
		previous = d.monitorEntry(previousID)
	}
	r := d.monitors()
	start := func() sessionmonitor.Start {
		return sessionmonitor.Start{Terminal: terminal, Conversation: conversation, Linked: linked, Candidate: candidate,
			Launch: func() sessionmonitor.Launch { return d.monitorLaunch(sessionID) }, Policy: d.monitorPolicyFrom(d.monitorPolicySources(), sessionID)}
	}
	r.policyMu.Lock()
	entry := r.entry(sessionID)
	reused := r.registry.Reuse(entry, start())
	r.policyMu.Unlock()
	if !reused {
		if entry != nil {
			entry.Stop()
		}
		r.policyMu.Lock()
		entry = r.registry.Start(entry, start())
		r.mu.Lock()
		if old := r.sessions[sessionID]; old != nil {
			old.stopCatchUp()
		}
		r.sessions[sessionID] = &monitorBinding{entry: entry, applied: entry.Snapshot()}
		homeOf(entry).bind(sessionID)
		r.mu.Unlock()
		r.policyMu.Unlock()
	}
	entry.Arm()
	if previous != nil && previous != entry {
		entry.TakeUserSubmit(previous)
	}
	return entry
}

func (d *Daemon) monitorLaunch(sessionID protocol.SessionID) sessionmonitor.Launch {
	session := d.store.Get(sessionID)
	if session == nil {
		return sessionmonitor.Launch{}
	}
	conversation := d.store.GetSessionConversation(sessionID)
	stamps := d.store.TurnStamps(sessionID)
	launch := sessionmonitor.Launch{
		Known:              true,
		Agent:              session.Agent,
		StartedAt:          d.store.SessionLaunchedAt(sessionID),
		Directory:          session.Directory,
		Branch:             branchFromSession(session),
		HarnessTitle:       d.store.SessionLabels(sessionID).HarnessTitle,
		State:              session.State,
		StateSince:         protocol.Timestamp(session.StateSince).Time(),
		StateUpdated:       protocol.Timestamp(session.StateUpdatedAt).Time(),
		LastModelRequestAt: protocol.Timestamp(protocol.Deref(session.LastModelRequestAt)).Time(),
		Turn: sessionmonitor.TurnStamps{OpenedAt: stamps.OpenedAt, SettledAt: stamps.SettledAt, SnoozedUntil: stamps.SnoozedUntil,
			AgentAt: stamps.AgentAt, RequestedAt: stamps.RequestedAt},
		Driver:       d.driverInputFromRecord(sessionID),
		Conversation: sessionmonitor.ConversationObservation{NativeID: conversation.NativeID, TranscriptPath: conversation.TranscriptPath},
	}
	if cost, err := d.store.SessionCostCheckpoint(sessionID); err == nil {
		launch.Cost = cost
	} else {
		d.logf("monitor cost: session=%s load failed: %v", sessionID, err)
	}
	if saved := d.store.GetSessionExitScreen(sessionID); saved != nil {
		launch.Exit = &sessionmonitor.Exit{Text: saved.Text, Cols: saved.Cols, Rows: saved.Rows, Code: saved.ExitCode,
			Signal: saved.ExitSignal, At: protocol.Timestamp(saved.ExitedAt).Time()}
	}
	return launch
}

func branchFromSession(session *protocol.Session) attngit.BranchInfo {
	return attngit.BranchInfo{Branch: protocol.Deref(session.Branch), IsWorktree: protocol.Deref(session.IsWorktree),
		MainRepo: protocol.Deref(session.MainRepo), Repository: protocol.Deref(session.Repository)}
}

func (d *Daemon) driverInputFromRecord(session protocol.SessionID) sessionmonitor.DriverMetadata {
	cursor := d.store.GetAgentDriverRun(session)
	return sessionmonitor.DriverMetadata{Plugin: cursor.PluginName, Run: cursor.RunID, Sequence: cursor.Seq,
		Metadata: d.store.GetAgentMetadata(session), TranscriptPath: d.store.GetAgentDriverTranscriptPath(session)}
}

func (d *Daemon) forgetMonitor(sessionID protocol.SessionID) {
	r := d.monitors()
	entry := r.entry(sessionID)
	if entry != nil {
		entry.Forget()
	}
	r.policyMu.Lock()
	r.mu.Lock()
	binding := r.sessions[sessionID]
	if binding != nil {
		binding.stopCatchUp()
		delete(r.sessions, sessionID)
	}
	r.mu.Unlock()
	r.policyMu.Unlock()
	if binding != nil && binding.entry != entry {
		binding.entry.Forget()
	}
}

type monitorHost struct{ daemon *Daemon }

func (h monitorHost) Go(name string, fn func()) bool { return h.daemon.life.Go(name, fn) }
func (h monitorHost) Do(name string, fn func()) bool { return h.daemon.life.Do(name, fn) }
func (h monitorHost) AfterFunc(name string, delay time.Duration, fn func()) *time.Timer {
	return h.daemon.life.AfterFunc(name, delay, fn)
}
func (h monitorHost) Context() context.Context        { return h.daemon.life.Context() }
func (h monitorHost) Done() <-chan struct{}           { return h.daemon.life.Done() }
func (h monitorHost) Ended() bool                     { return h.daemon.life.Ended() }
func (h monitorHost) Logf(format string, args ...any) { h.daemon.logf(format, args...) }
func (h monitorHost) Debug() bool                     { return h.daemon.debugLogging }
func (h monitorHost) HomeFor(entry *sessionmonitor.Entry) sessionmonitor.Home {
	return &monitorHome{daemon: h.daemon, entry: entry}
}
func (h monitorHost) Classify(ctx context.Context, request sessionmonitor.ClassifyRequest) (string, error) {
	return h.daemon.runClassifier(ctx, request.Agent, request.Directory, request.Text, request.Timeout)
}
func (h monitorHost) ReadBranch(ctx context.Context, directory string) (*attngit.BranchInfo, error) {
	return h.daemon.readBranchInfo(ctx, gitTask{Kind: gitTaskSessionIdentity, Lane: gitDeferred}, directory)
}

type monitorHome struct {
	daemon  *Daemon
	entry   *sessionmonitor.Entry
	session atomic.Value
}

func homeOf(entry *sessionmonitor.Entry) *monitorHome { return entry.Home().(*monitorHome) }

func (h *monitorHome) bind(sessionID protocol.SessionID) {
	h.session.Store(sessionID)
	h.entry.Bind(string(sessionID))
}

func (h *monitorHome) sessionID() protocol.SessionID {
	id, _ := h.session.Load().(protocol.SessionID)
	return id
}

func (h *monitorHome) SnoozeWoke(at time.Time, cause string) {
	if id := h.sessionID(); id != "" {
		h.daemon.finishSnoozeWake(id, at, cause)
	}
}

func (h *monitorHome) PrimaryTerminal() harness.TerminalID {
	return h.daemon.primaryTerminal(h.sessionID())
}

func (h *monitorHome) Input(ctx context.Context, terminal harness.TerminalID, data []byte) error {
	if h.daemon.ptyBackend == nil {
		return errNoPTYBackend
	}
	return h.daemon.ptyBackend.Input(ctx, terminal, data)
}

func (h *monitorHome) Link(voice harness.Voice) harness.Link {
	session := h.daemon.store.Get(h.sessionID())
	if session == nil {
		return nil
	}
	link := h.daemon.sessionLink(session, voice)
	if link == nil {
		return nil
	}
	return sessionLinkTo{Link: link, session: session.ID}
}

type sessionLinkTo struct {
	harness.Link
	session protocol.SessionID
}

func (l sessionLinkTo) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	in.Session = l.session
	return l.Link.Deliver(ctx, in)
}

func (h *monitorHome) NoteKey(terminal harness.TerminalID, at time.Time) {
	h.daemon.terminals().noteKey(terminal, at)
}

func (h *monitorHome) LastKey() time.Time {
	return h.daemon.terminals().lastKeyOf(h.PrimaryTerminal())
}

func (h *monitorHome) EnqueueTitle(conversation, source string) error {
	return h.daemon.enqueueSessionTitle(h.sessionID(), conversation, source)
}

func (h *monitorHome) Conversation() sessionmonitor.ConversationObservation {
	binding := h.daemon.store.GetSessionConversation(h.sessionID())
	return sessionmonitor.ConversationObservation{NativeID: binding.NativeID, TranscriptPath: binding.TranscriptPath}
}

func (h *monitorHome) ConversationClaimedElsewhere(nativeID string) bool {
	return h.daemon.store.ConversationBoundToOtherSession(h.sessionID(), nativeID)
}

func (h *monitorHome) LinkTranscriptPath(path string) string {
	if l := h.daemon.linkOf(h.sessionID()); l != nil {
		return l.transcriptPath(path)
	}
	return path
}

func (h *monitorHome) AssistantWindowChanged() {
	h.daemon.publishFact(FactSessionAssistantWindowChanged, string(h.sessionID()), nil)
}

func (h *monitorHome) LastSeen() string {
	if session := h.daemon.store.Get(h.sessionID()); session != nil {
		return session.LastSeen
	}
	return ""
}

package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

type monitorBinding struct {
	copied     bool
	monitor    *sessionmonitor.Monitor
	applied    sessionmonitor.View
	catchUp    *time.Timer
	retryDelay time.Duration
}

type monitorIndex struct {
	policyMu     sync.Mutex
	applyMu      sync.Mutex
	mu           sync.Mutex
	sessions     map[protocol.SessionID]*monitorBinding
	registry     *sessionmonitor.Registry
	viewsChanged chan struct{}
}

func (d *Daemon) monitors() *monitorIndex {
	d.monitorsOnce.Do(func() {
		d.monitorState = &monitorIndex{sessions: make(map[protocol.SessionID]*monitorBinding), viewsChanged: make(chan struct{}), registry: sessionmonitor.NewRegistry(monitorHost{daemon: d}, d.monitorSaver())}
	})
	return d.monitorState
}

func (r *monitorIndex) monitor(sessionID protocol.SessionID) *sessionmonitor.Monitor {
	r.mu.Lock()
	defer r.mu.Unlock()
	if binding := r.sessions[sessionID]; binding != nil {
		return binding.monitor
	}
	return nil
}

func (d *Daemon) sessionMonitor(sessionID protocol.SessionID) *sessionmonitor.Monitor {
	return d.monitors().monitor(sessionID)
}

func (r *monitorIndex) viewChanges() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.viewsChanged
}

func (r *monitorIndex) notifyViewsChanged() {
	r.mu.Lock()
	defer r.mu.Unlock()
	close(r.viewsChanged)
	r.viewsChanged = make(chan struct{})
}

func (r *monitorIndex) each() map[protocol.SessionID]*sessionmonitor.Monitor {
	r.mu.Lock()
	defer r.mu.Unlock()
	monitors := make(map[protocol.SessionID]*sessionmonitor.Monitor, len(r.sessions))
	for id, binding := range r.sessions {
		if binding.monitor != nil {
			monitors[id] = binding.monitor
		}
	}
	return monitors
}

func (d *Daemon) startSessionMonitor(sessionID protocol.SessionID) *sessionmonitor.Monitor {
	return d.startSessionMonitorFrom(sessionID, "", nil)
}

func (d *Daemon) startSessionMonitorSeeded(seed *protocol.Session, fresh sessionmonitor.Evidence) *sessionmonitor.Monitor {
	monitor := d.startSessionMonitor(seed.ID)
	monitor.Restart(withSeed(d.monitorLaunch(seed.ID), seed), fresh)
	return monitor
}

func (d *Daemon) startSessionMonitorFrom(sessionID, previousID protocol.SessionID, candidate *sessionmonitor.Monitor) *sessionmonitor.Monitor {
	if d.sessionHost(sessionID) != "" {
		return nil
	}
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
	var previous *sessionmonitor.Monitor
	if previousID != "" {
		previous = d.sessionMonitor(previousID)
	}
	r := d.monitors()
	start := func() sessionmonitor.Start {
		return sessionmonitor.Start{Session: sessionID, Terminal: terminal, Conversation: conversation, Linked: linked, Candidate: candidate,
			Launch: func() sessionmonitor.Launch { return d.monitorLaunch(sessionID) }, Policy: d.monitorPolicyFrom(d.monitorPolicySources(), sessionID)}
	}
	r.policyMu.Lock()
	monitor := r.monitor(sessionID)
	reused := r.registry.Reuse(monitor, start())
	r.policyMu.Unlock()
	if !reused {
		if monitor != nil {
			monitor.Stop()
		}
		r.policyMu.Lock()
		monitor = r.registry.Start(monitor, start())
		r.mu.Lock()
		if old := r.sessions[sessionID]; old != nil {
			old.stopCatchUp()
		}
		applied := monitor.Snapshot()
		applied.PullRequests.Entries = d.recordedLinks(sessionID)
		r.sessions[sessionID] = &monitorBinding{monitor: monitor, applied: applied, copied: d.copiedSession(sessionID)}
		homeOf(monitor).bind(sessionID)
		r.mu.Unlock()
		r.policyMu.Unlock()
	}
	monitor.SetTerminals(string(sessionID), d.terminals().Of(sessionID))
	d.reportTerminalTitle(sessionID)
	monitor.Arm()
	d.syncMonitorConversation(sessionID)
	if previous != nil && previous != monitor {
		monitor.TakeUserSubmit(previous)
	}
	return monitor
}

func withSeed(launch sessionmonitor.Launch, seed *protocol.Session) sessionmonitor.Launch {
	launch.Branch = branchFromSession(seed)
	launch.State = seed.State
	launch.StateSince = protocol.Timestamp(seed.StateSince).Time()
	launch.StateUpdated = protocol.Timestamp(seed.StateUpdatedAt).Time()
	launch.LastModelRequestAt = protocol.Timestamp(protocol.Deref(seed.LastModelRequestAt)).Time()
	if launch.LastModelRequestAt.IsZero() {
		launch.LastModelRequestAt = launch.StateUpdated
	}
	launch.LastSeen = protocol.Timestamp(seed.LastSeen).Time()
	return launch
}

func (d *Daemon) monitorLaunch(sessionID protocol.SessionID) sessionmonitor.Launch {
	session := d.store.Get(sessionID)
	if session == nil {
		return sessionmonitor.Launch{}
	}
	conversation := d.store.GetSessionConversation(sessionID)
	stamps := d.store.TurnStamps(sessionID)
	records := d.store.SessionRuntimeRecords(sessionID)
	launch := sessionmonitor.Launch{
		Known:              true,
		Agent:              session.Agent,
		Link:               d.store.SessionLink(sessionID),
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
		Driver:        d.driverInputFromRecord(sessionID),
		Conversation:  sessionmonitor.ConversationObservation{NativeID: conversation.NativeID, TranscriptPath: conversation.TranscriptPath},
		LastSeen:      protocol.Timestamp(session.LastSeen).Time(),
		PendingSwitch: decodePendingSwitch(records.PendingSwitch),
		PullRequests:  decodePullRequests(records.PullRequests),
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
	monitor := r.monitor(sessionID)
	if monitor != nil {
		monitor.Forget()
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
	if binding != nil && binding.monitor != monitor {
		binding.monitor.Forget()
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
func (h monitorHost) HomeFor(monitor *sessionmonitor.Monitor) sessionmonitor.Home {
	return &monitorHome{daemon: h.daemon, monitor: monitor}
}
func (h monitorHost) Classify(ctx context.Context, request sessionmonitor.ClassifyRequest) (string, error) {
	return h.daemon.runClassifier(ctx, request.Agent, request.Directory, request.Text, request.Timeout)
}
func (h monitorHost) ReadBranch(ctx context.Context, directory string) (*attngit.BranchInfo, error) {
	return h.daemon.readBranchInfo(ctx, gitTask{Kind: gitTaskSessionIdentity, Lane: gitDeferred}, directory)
}

type monitorHome struct {
	bridge  *bridge.Conn
	host    string
	daemon  *Daemon
	monitor *sessionmonitor.Monitor
	session atomic.Value
}

func homeOf(monitor *sessionmonitor.Monitor) *monitorHome { return monitor.Home().(*monitorHome) }

func (h *monitorHome) bind(sessionID protocol.SessionID) {
	h.session.Store(sessionID)
	h.monitor.Bind(string(sessionID))
}

func (h *monitorHome) sessionID() protocol.SessionID {
	id, _ := h.session.Load().(protocol.SessionID)
	return id
}

func (h monitorHost) session(monitor *sessionmonitor.Monitor) protocol.SessionID {
	return homeOf(monitor).sessionID()
}

func (h monitorHost) LiveTerminals(ctx context.Context) map[harness.TerminalID]struct{} {
	return h.daemon.liveTerminals(ctx)
}

func (h monitorHost) ViewOwner(terminal harness.TerminalID) string {
	session, _ := h.daemon.shownIn(terminal)
	return string(session)
}

func (h monitorHost) Input(ctx context.Context, terminal harness.TerminalID, data []byte) error {
	if h.daemon.ptyBackend == nil {
		return errNoPTYBackend
	}
	return h.daemon.ptyBackend.Input(ctx, terminal, data)
}

func (h monitorHost) Link(monitor *sessionmonitor.Monitor, voice harness.Voice) harness.Link {
	session := h.daemon.store.Get(h.session(monitor))
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

func (h monitorHost) LinkTranscriptPath(link, path string) string {
	if l := h.daemon.linkNamed(link); l != nil {
		return l.transcriptPath(path)
	}
	return path
}

func (h monitorHost) NoteKey(terminal harness.TerminalID, at time.Time) {
	h.daemon.terminals().noteKey(terminal, at)
}

func (h monitorHost) LastKey(terminal harness.TerminalID) time.Time {
	return h.daemon.terminals().lastKeyOf(terminal)
}

func (h monitorHost) EnqueueTitle(monitor *sessionmonitor.Monitor, conversation, source string) error {
	return h.daemon.enqueueSessionTitle(h.session(monitor), conversation, source)
}

func (d *Daemon) kickMonitorTerminals() {
	if !d.monitorTerminalsDirty.CompareAndSwap(false, true) {
		return
	}
	d.life.Go("pushMonitorTerminals", func() {
		d.monitorTerminalsDirty.Store(false)
		for sessionID, monitor := range d.monitors().each() {
			if d.store.Get(sessionID) == nil {
				r := d.monitors()
				r.mu.Lock()
				binding := r.sessions[sessionID]
				copied := binding != nil && binding.copied
				r.mu.Unlock()
				if copied {
					d.forgetMonitor(sessionID)
					continue
				}
			}
			monitor.SetTerminals(string(sessionID), d.terminals().Of(sessionID))
		}
	})
}

func (d *Daemon) monitorSaver() sessionmonitor.Saver {
	if d.store == nil {
		return nil
	}
	return storeSaver{daemon: d}
}

func (d *Daemon) recordedLinks(sessionID protocol.SessionID) map[string]sessionmonitor.PullRequest {
	records := d.store.ListSessionPullRequests(sessionID)
	if len(records) == 0 {
		return nil
	}
	links := make(map[string]sessionmonitor.PullRequest, len(records))
	for _, record := range records {
		links[record.PRID] = sessionmonitor.PullRequest{Repository: record.Repository, Number: record.Number, URL: record.URL}
	}
	return links
}

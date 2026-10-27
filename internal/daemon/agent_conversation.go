package daemon

import (
	"context"
	"errors"
	"net"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
)

type agentConversationObservation struct {
	SessionID      protocol.SessionID `json:"-"`
	NativeID       string             `json:"-"`
	TranscriptPath string             `json:"transcript_path,omitempty"`
}

func (d *Daemon) handleObserveAgentConversation(ctx context.Context, conn net.Conn, msg *protocol.SetSessionResumeIDMessage) {
	terminal := protocol.TrimID(msg.ID)
	observation := agentConversationObservation{
		NativeID:       strings.TrimSpace(msg.ResumeSessionID),
		TranscriptPath: strings.TrimSpace(protocol.Deref(msg.TranscriptPath)),
	}
	if terminal == "" {
		d.sendError(conn, "missing id")
		return
	}
	if observation.NativeID == "" {
		d.sendError(conn, "missing resume_session_id")
		return
	}
	if observation.TranscriptPath == "" {
		d.logf("agent conversation: ignored pathless observation terminal=%s native=%s", terminal, observation.NativeID)
		d.sendOK(conn)
		return
	}
	if _, view, ours := d.linkCaller(terminal); ours {
		if view == "" {
			d.logf("agent conversation: no terminal shows the conversation of %s", terminal)
			d.sendOK(conn)
			return
		}
		terminal = view
	}
	if held := d.store.GetSessionConversation(d.sessionInTerminal(terminal)).NativeID; held != "" && held != observation.NativeID {
		pausepoint.At(pausepoint.ConversationSwitchObserved)
	}
	receipt, accepted := d.conversationIn(terminal, observation)
	if !accepted {
		d.sendError(conn, "conversation observation could not be saved")
		return
	}
	if receipt != nil && !receipt.Switching() {
		ctx, cancel := d.connectionContext(ctx, conn)
		defer cancel()
		if err := receipt.Wait(ctx); err != nil {
			d.sendError(conn, err.Error())
			return
		}
	}
	d.sendOK(conn)
}

// conversationIn routes a conversation the harness in terminal t reports from SessionStart or
// UserPromptSubmit, or a shared Codex terminal from the reply that switches it. Those arrive in
// order, so only they may open or show a session.
func (d *Daemon) conversationIn(t harness.TerminalID, observation agentConversationObservation) (*sessionmonitor.ConversationReceipt, bool) {
	observation.SessionID = d.sessionInTerminal(t)
	return d.proposeMonitorConversation(observation, sessionmonitor.ConversationObserved, t)
}

func (d *Daemon) acceptMonitorConversation(source *sessionmonitor.Monitor, proposed sessionmonitor.Conversation, observation agentConversationObservation) bool {
	t := proposed.Terminal
	cur, placed := d.terminals().Showing(t)
	if current := d.store.Get(cur); !placed || (current != nil && !conversationIsSession(current.Agent)) {
		observation.SessionID = d.sessionInTerminal(t)
		defer d.lockSessionLifecycles(observation.SessionID)()
		if source != nil {
			if pending, _ := source.PendingSwitch(); pending != proposed {
				return false
			}
		}
		return d.observeOrQueueAgentConversation(observation)
	}
	held := d.store.GetSessionConversation(cur).NativeID
	var owner protocol.SessionID
	if held != observation.NativeID {
		owner = d.store.ConversationOwner(cur, observation.NativeID)
	}
	if held != "" && held != observation.NativeID {
		pausepoint.At(pausepoint.ConversationOwnerResolved)
	}
	defer d.lockSessionLifecycles(cur, owner)()
	defer d.lockTerminalEnds(cur)()
	if source != nil {
		if pending, _ := source.PendingSwitch(); pending != proposed {
			return false
		}
	}
	session := d.store.Get(cur)
	if shown, _ := d.terminals().Showing(t); shown != cur || session == nil || !d.terminalLive(t) {
		d.logf("agent conversation: dropped %s from terminal %s, which no longer runs session %s", observation.NativeID, t, cur)
		return false
	}
	observation.SessionID = session.ID
	if held = d.store.GetSessionConversation(session.ID).NativeID; held == observation.NativeID {
		return d.applyAgentConversation(observation, d.store.TransitionSessionConversation)
	}
	if now := d.store.ConversationOwner(session.ID, observation.NativeID); now != owner {
		d.logf("agent conversation: dropped %s from terminal %s; its owner changed from %q to %q meanwhile", observation.NativeID, t, owner, now)
		return false
	}
	var err error
	switch {
	case owner == "" && held == "":
		return d.applyAgentConversation(observation, d.store.TransitionSessionConversation)
	case owner == "":
		err = d.opened(t, session, observation)
	case d.ownerLive(owner) && !d.conversationShowsInManyTerminals(session.ID):
		return d.applyAgentConversation(observation, d.store.TransitionSessionConversation)
	default:
		err = d.shows(t, session, owner, observation)
	}
	if err != nil {
		d.logf("agent conversation: terminal %s could not move on from session %s for %s; it keeps it: %v", t, session.ID, observation.NativeID, err)
	}

	return err == nil
}

func (d *Daemon) ownerLive(owner protocol.SessionID) bool {
	return d.sessionHasLiveWorker(owner)
}

func conversationIsSession(agent protocol.SessionAgent) bool {
	return conversationDecidesIdentity(agentdriver.Get(string(agent)))
}

func conversationDecidesIdentity(driver agentdriver.Driver) bool {
	return agentdriver.EffectiveCapabilities(driver).ConversationIsSession
}

func (d *Daemon) observeOrQueueAgentConversation(observation agentConversationObservation) bool {
	m := d.sessionMonitor(observation.SessionID)
	if m == nil {
		return false
	}
	if m.QueueConversationWhile(func() bool { return d.store.Get(observation.SessionID) == nil }, observation.monitored()) {
		return true
	}
	return d.applyAgentConversation(observation, d.store.TransitionSessionConversation)
}

func (d *Daemon) consumePendingAgentConversation(sessionID protocol.SessionID) (agentConversationObservation, bool) {
	sessionID = protocol.TrimID(sessionID)
	observation, ok := d.sessionMonitor(sessionID).TakePendingConversation()
	if !ok {
		return agentConversationObservation{}, false
	}
	return agentConversationObservation{SessionID: sessionID, NativeID: observation.NativeID, TranscriptPath: observation.TranscriptPath}, true
}

func (o agentConversationObservation) monitored() sessionmonitor.ConversationObservation {
	return sessionmonitor.ConversationObservation{NativeID: o.NativeID, TranscriptPath: o.TranscriptPath}
}

func (d *Daemon) proposeMonitorConversation(observation agentConversationObservation, proposal sessionmonitor.ConversationProposal, terminal harness.TerminalID) (*sessionmonitor.ConversationReceipt, bool) {
	return d.monitorInTerminal(terminal, observation.SessionID).ProposeConversation(observation.monitored(), proposal, terminal)
}

func (d *Daemon) observeAgentConversation(observation agentConversationObservation) {
	d.proposeMonitorConversation(observation, sessionmonitor.ConversationObserved, "")
}

func (d *Daemon) claimAgentConversation(observation agentConversationObservation) (*sessionmonitor.ConversationReceipt, bool) {
	return d.proposeMonitorConversation(observation, sessionmonitor.ConversationReported, "")
}

func (d *Daemon) applyAgentConversation(observation agentConversationObservation, transition func(sessionID protocol.SessionID, nativeID string, transcriptPath string) (bool, error)) bool {
	changed, err := transition(observation.SessionID, observation.NativeID, observation.TranscriptPath)
	if errors.Is(err, store.ErrConversationClaimed) {
		d.logf("agent conversation: %s is bound to another session, session=%s", observation.NativeID, observation.SessionID)
		return false
	}
	if err != nil {
		d.logf("agent conversation: transition failed session=%s native=%s: %v", observation.SessionID, observation.NativeID, err)
		return false
	}
	if !changed {
		return true
	}

	d.resetSessionActivityRuntime(observation.SessionID)
	d.publishFact(FactSessionConversationChanged, string(observation.SessionID), observation)
	d.mirrorLabel(observation.SessionID)
	return true
}

func (d *Daemon) resetSessionActivityRuntime(sessionID protocol.SessionID) {
	d.sessionActivityRunsMu.Lock()
	delete(d.sessionActivityRuns, sessionID)
	d.sessionActivityRunsMu.Unlock()
}

func (d *Daemon) replayPendingSwitches() {
	for id, raw := range d.store.PendingSessionSwitches() {
		if d.sessionMonitor(id) != nil {
			continue
		}
		pending := decodePendingSwitch(raw)
		if len(pending) == 0 {
			continue
		}
		shown, ok := d.shownIn(pending[0].Terminal)
		if !ok {
			continue
		}
		launch := d.monitorLaunch(shown)
		launch.PendingSwitch = pending
		conversation := d.store.GetSessionConversation(id)
		if saved := d.store.SessionRuntimeRecords(id); saved.HomeDaemonID != "" {
			conversation.NativeID, conversation.TranscriptPath = saved.ConversationNativeID, saved.ConversationTranscriptPath
		}
		launch.Conversation = sessionmonitor.ConversationObservation{NativeID: conversation.NativeID, TranscriptPath: conversation.TranscriptPath}
		if cost, err := d.store.SessionCostCheckpoint(id); err == nil {
			launch.Cost = cost
		}
		r := d.monitors()
		monitor := r.registry.Start(nil, sessionmonitor.Start{Session: id, Terminal: pending[0].Terminal, Conversation: conversation.NativeID, Linked: launch.Link == codexLinkKind, Launch: func() sessionmonitor.Launch { return launch }, Policy: d.monitorPolicyFrom(d.monitorPolicySources(), shown)})
		r.mu.Lock()
		r.sessions[id] = &monitorBinding{monitor: monitor, applied: monitor.Snapshot(), copied: d.copiedSession(id)}
		homeOf(monitor).bind(id)
		r.mu.Unlock()
	}

	for _, monitor := range d.monitors().each() {
		monitor.RebuildCandidates()
		if _, ok := monitor.PendingSwitch(); ok {
			monitor.Refresh()
		}
	}
}

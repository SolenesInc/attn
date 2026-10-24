package daemon

import (
	"errors"
	"net"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
)

type agentConversationObservation struct {
	SessionID      protocol.SessionID `json:"-"`
	NativeID       string             `json:"-"`
	TranscriptPath string             `json:"transcript_path,omitempty"`
}

func (d *Daemon) handleObserveAgentConversation(conn net.Conn, msg *protocol.SetSessionResumeIDMessage) {
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
	d.conversationIn(terminal, observation)
	d.sendOK(conn)
}

// conversationIn routes a conversation the harness in terminal t reports from SessionStart or
// UserPromptSubmit, or a shared Codex terminal from the reply that switches it. Those arrive in
// order, so only they may open or show a session.
func (d *Daemon) conversationIn(t harness.TerminalID, observation agentConversationObservation) {
	observation.SessionID = d.sessionInTerminal(t)
	d.proposeMonitorConversation(observation, sessionmonitor.ConversationObserved, t)
}

func (d *Daemon) acceptMonitorConversation(t harness.TerminalID, observation agentConversationObservation, candidate *sessionmonitor.Entry) bool {
	cur, placed := d.terminals().Showing(t)
	if current := d.store.Get(cur); !placed || (current != nil && !conversationIsSession(current.Agent)) {
		observation.SessionID = d.sessionInTerminal(t)
		return d.observeOrQueueAgentConversation(observation, candidate)
	}
	held := d.store.GetSessionConversation(cur).NativeID
	var owner protocol.SessionID
	if held != observation.NativeID {
		owner = d.store.ConversationOwner(cur, observation.NativeID)
	}
	defer d.lockSessionLifecycles(cur, owner)()
	session := d.store.Get(cur)
	if shown, _ := d.terminals().Showing(t); shown != cur || session == nil || !d.terminalLive(t) {
		d.logf("agent conversation: dropped %s from terminal %s, which no longer runs session %s", observation.NativeID, t, cur)
		return false
	}
	observation.SessionID = session.ID
	if held = d.store.GetSessionConversation(session.ID).NativeID; held == observation.NativeID {
		return d.applyAgentConversation(observation, d.store.TransitionSessionConversation, candidate)
	}
	if now := d.store.ConversationOwner(session.ID, observation.NativeID); now != owner {
		d.logf("agent conversation: dropped %s from terminal %s; its owner changed from %q to %q meanwhile", observation.NativeID, t, owner, now)
		return false
	}
	var err error
	switch {
	case owner == "" && held == "":
		return d.applyAgentConversation(observation, d.store.TransitionSessionConversation, candidate)
	case owner == "":
		err = d.opened(t, session, observation, candidate)
	case d.ownerLive(owner) && d.linkOf(session.ID) == nil:
		// A live owner keeps its terminal, so this session takes the conversation over in place.
		return d.applyAgentConversation(observation, d.store.TransitionSessionConversation, candidate)
	default:
		err = d.shows(t, session, owner, observation, candidate)
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

func (d *Daemon) observeOrQueueAgentConversation(observation agentConversationObservation, candidate *sessionmonitor.Entry) bool {
	e := d.monitorEntry(observation.SessionID)
	if e == nil {
		return false
	}
	if e.QueueConversationWhile(func() bool { return d.store.Get(observation.SessionID) == nil }, observation.monitored()) {
		return true
	}
	return d.applyAgentConversation(observation, d.store.TransitionSessionConversation, candidate)
}

func (d *Daemon) consumePendingAgentConversation(sessionID protocol.SessionID) (agentConversationObservation, bool) {
	sessionID = protocol.TrimID(sessionID)
	observation, ok := d.monitorEntry(sessionID).TakePendingConversation()
	if !ok {
		return agentConversationObservation{}, false
	}
	return agentConversationObservation{SessionID: sessionID, NativeID: observation.NativeID, TranscriptPath: observation.TranscriptPath}, true
}

func (o agentConversationObservation) monitored() sessionmonitor.ConversationObservation {
	return sessionmonitor.ConversationObservation{NativeID: o.NativeID, TranscriptPath: o.TranscriptPath}
}

func (d *Daemon) proposeMonitorConversation(observation agentConversationObservation, proposal sessionmonitor.ConversationProposal, terminal harness.TerminalID) bool {
	return d.monitorEntry(observation.SessionID).ProposeConversation(observation.monitored(), proposal, terminal)
}

func (d *Daemon) admitMonitorConversation(entry *sessionmonitor.Entry, view sessionmonitor.View) bool {
	if view.Succeeds.V == (sessionmonitor.Key{}) {
		d.logf("monitor apply: unknown entry %+v without predecessor", view.Key.V)
		return false
	}
	var session protocol.SessionID
	for id, candidate := range d.monitors().each() {
		if candidate.Key() == view.Succeeds.V {
			session = id
			break
		}
	}
	if session == "" || view.Conversation.V.NativeID == "" {
		d.logf("monitor apply: unknown entry %+v with predecessor %+v", view.Key.V, view.Succeeds.V)
		return false
	}
	conversation := view.Conversation.V
	observation := agentConversationObservation{SessionID: session, NativeID: conversation.NativeID, TranscriptPath: conversation.TranscriptPath}
	if conversation.Terminal != "" {
		return d.acceptMonitorConversation(conversation.Terminal, observation, entry)
	}
	switch conversation.Proposal {
	case sessionmonitor.ConversationReported:
		return d.applyAgentConversation(observation, d.store.ClaimSessionConversation, entry)
	case sessionmonitor.ConversationGuessedAtLaunch:
		guess := func(sessionID protocol.SessionID, nativeID, transcriptPath string) (bool, error) {
			changed, err := d.store.ClaimLaunchConversation(sessionID, nativeID, transcriptPath)
			if errors.Is(err, store.ErrConversationClaimed) {
				d.monitorEntry(sessionID).ConversationClaimedElsewhere(nativeID)
			}
			return changed, err
		}
		return d.applyAgentConversation(observation, guess, entry)
	}
	return d.applyAgentConversation(observation, d.store.TransitionSessionConversation, entry)
}

func (d *Daemon) observeAgentConversation(observation agentConversationObservation) {
	d.proposeMonitorConversation(observation, sessionmonitor.ConversationObserved, "")
}

func (d *Daemon) claimAgentConversation(observation agentConversationObservation) bool {
	return d.proposeMonitorConversation(observation, sessionmonitor.ConversationReported, "")
}

func (d *Daemon) applyAgentConversation(observation agentConversationObservation, transition func(sessionID protocol.SessionID, nativeID string, transcriptPath string) (bool, error), candidate *sessionmonitor.Entry) bool {
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
		d.syncMonitorConversation(observation.SessionID)
		d.monitorEntry(observation.SessionID).EnsureTranscriptWatcher(observation.TranscriptPath)
		return true
	}

	entry := d.startSessionMonitorFrom(observation.SessionID, "", candidate)
	entry.SetConversation(observation.monitored())
	d.rememberDispatchResume(observation.SessionID, observation.NativeID)
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

func (d *Daemon) subscribeAgentConversationFacts() {
	if d.eventBus == nil || d.conversationUnsubHooks != nil {
		return
	}
	d.conversationUnsubHooks = d.eventBus.Subscribe(
		bus.Filter{FactSessionConversationChanged},
		d.rebindTranscriptWatcherForConversation,
	)
}

func (d *Daemon) unsubscribeAgentConversationFacts() {
	if d.conversationUnsubHooks != nil {
		d.conversationUnsubHooks()
		d.conversationUnsubHooks = nil
	}
}

func (d *Daemon) rebindTranscriptWatcherForConversation(event bus.Event) {
	session := d.store.Get(protocol.SessionID(event.Subject))
	if session == nil || !sessionmonitor.IsTranscriptWatchedAgent(session.Agent) {
		return
	}
	var observation agentConversationObservation
	if err := event.Decode(&observation); err != nil {
		d.logf("agent conversation: decode fact for session %s: %v", event.Subject, err)
	}
	d.monitorEntry(session.ID).StartTranscriptWatcher(observation.TranscriptPath)
}

func (d *Daemon) replayPendingSwitches() {
	for sessionID, entry := range d.monitors().each() {
		pending, ok := entry.PendingSwitch()
		if !ok {
			continue
		}
		d.logf("agent conversation: replaying the switch session %s saved to %s before the restart", sessionID, pending.NativeID)
		if pending.Terminal == "" {
			entry.ObserveResumeID(pending.NativeID)
			continue
		}
		d.conversationIn(pending.Terminal, agentConversationObservation{NativeID: pending.NativeID, TranscriptPath: pending.TranscriptPath})
	}
}

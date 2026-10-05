package daemon

import (
	"errors"
	"net"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type agentConversationObservation struct {
	SessionID      string `json:"-"`
	NativeID       string `json:"-"`
	TranscriptPath string `json:"transcript_path,omitempty"`
}

func (d *Daemon) handleObserveAgentConversation(conn net.Conn, msg *protocol.SetSessionResumeIDMessage) {
	terminal := harness.TerminalID(strings.TrimSpace(msg.ID))
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
	if conversation, ok := strings.CutPrefix(string(terminal), hooks.CodexThreadCallerPrefix); ok {
		shown, showing := d.codexShared().terminalShowing(conversation)
		if !showing {
			d.logf("agent conversation: no terminal shows shared Codex conversation %s", conversation)
			d.sendOK(conn)
			return
		}
		terminal = shown
	}
	d.conversationIn(terminal, observation)
	d.sendOK(conn)
}

// conversationIn routes a conversation the harness in terminal t reports from SessionStart or
// UserPromptSubmit. Those arrive in order, so only they may open or show a session.
func (d *Daemon) conversationIn(t harness.TerminalID, observation agentConversationObservation) {
	cur, placed := d.terminals().Showing(t)
	if current := d.store.Get(string(cur)); !placed || (current != nil && !conversationIsSession(current.Agent)) {
		observation.SessionID = d.callerID(string(t))
		d.observeOrQueueAgentConversation(observation)
		return
	}
	held := d.store.GetSessionConversation(string(cur)).NativeID
	owner := ""
	if held != observation.NativeID {
		owner = d.store.ConversationOwner(string(cur), observation.NativeID)
	}
	defer d.lockSessionLifecycles(string(cur), owner)()
	session := d.store.Get(string(cur))
	if shown, _ := d.terminals().Showing(t); shown != cur || session == nil || !d.terminalLive(t) {
		d.logf("agent conversation: dropped %s from terminal %s, which no longer runs session %s", observation.NativeID, t, cur)
		return
	}
	observation.SessionID = session.ID
	if held = d.store.GetSessionConversation(session.ID).NativeID; held == observation.NativeID {
		d.observeAgentConversation(observation)
		return
	}
	if now := d.store.ConversationOwner(session.ID, observation.NativeID); now != owner {
		d.logf("agent conversation: dropped %s from terminal %s; its owner changed from %q to %q meanwhile", observation.NativeID, t, owner, now)
		return
	}
	var err error
	switch {
	case owner == "" && held == "":
		d.observeAgentConversation(observation)
	case owner == "":
		err = d.opened(t, session, observation)
	case d.ownerLive(owner) && !d.codexShared().launchedShared(session.ID):
		// A live owner keeps its terminal, so this session takes the conversation over in place.
		d.observeAgentConversation(observation)
	default:
		err = d.shows(t, session, owner, observation)
	}
	if err != nil {
		d.logf("agent conversation: terminal %s could not move on from session %s for %s; it keeps it: %v", t, session.ID, observation.NativeID, err)
	}
}

func (d *Daemon) ownerLive(owner string) bool {
	return d.sessionHasLiveWorker(owner)
}

func conversationIsSession(agent protocol.SessionAgent) bool {
	return conversationDecidesIdentity(agentdriver.Get(string(agent)))
}

func conversationDecidesIdentity(driver agentdriver.Driver) bool {
	return agentdriver.EffectiveCapabilities(driver).ConversationIsSession
}

func (d *Daemon) observeOrQueueAgentConversation(observation agentConversationObservation) {
	d.pendingConversationMu.Lock()
	if d.store.Get(observation.SessionID) == nil {
		if d.pendingConversation == nil {
			d.pendingConversation = make(map[string]agentConversationObservation)
		}
		d.pendingConversation[observation.SessionID] = observation
		d.pendingConversationMu.Unlock()
		return
	}
	d.pendingConversationMu.Unlock()
	d.observeAgentConversation(observation)
}

func (d *Daemon) consumePendingAgentConversation(sessionID string) (agentConversationObservation, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return agentConversationObservation{}, false
	}
	d.pendingConversationMu.Lock()
	defer d.pendingConversationMu.Unlock()
	observation, ok := d.pendingConversation[sessionID]
	delete(d.pendingConversation, sessionID)
	return observation, ok
}

func (d *Daemon) observeAgentConversation(observation agentConversationObservation) {
	d.applyAgentConversation(observation, d.store.TransitionSessionConversation)
}

func (d *Daemon) claimAgentConversation(observation agentConversationObservation) bool {
	return d.applyAgentConversation(observation, d.store.ClaimSessionConversation)
}

func (d *Daemon) applyAgentConversation(observation agentConversationObservation, transition func(sessionID, nativeID, transcriptPath string) (bool, error)) bool {
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
		d.ensureTranscriptWatcherAtPath(observation.SessionID, observation.TranscriptPath)
		return true
	}

	d.rememberDispatchResume(observation.SessionID, observation.NativeID)
	d.resetSessionActivityRuntime(observation.SessionID)
	d.publishFact(FactSessionConversationChanged, observation.SessionID, observation)
	if session := d.store.Get(observation.SessionID); session != nil && session.Agent == protocol.SessionAgentCodex &&
		!sessionLabelIsPlaceholder(session.Label, session.Directory, session.ID) {
		d.codexShared().mirrorName(session.ID, session.Label)
	}
	return true
}

func (d *Daemon) resetSessionActivityRuntime(sessionID string) {
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
	session := d.store.Get(event.Subject)
	if session == nil || !isTranscriptWatchedAgent(session.Agent) {
		return
	}
	var observation agentConversationObservation
	if err := event.Decode(&observation); err != nil {
		d.logf("agent conversation: decode fact for session %s: %v", event.Subject, err)
	}
	d.startTranscriptWatcherAtPath(
		session.ID,
		session.Agent,
		session.Directory,
		d.sessionStartedAt(session.ID),
		observation.TranscriptPath,
	)
}

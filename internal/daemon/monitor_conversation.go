package daemon

import (
	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) proposeMonitorConversation(observation agentConversationObservation, claim bool, terminal harness.TerminalID) bool {
	previous := d.monitorEntry(observation.SessionID)
	if previous == nil {
		return false
	}
	previous.mu.Lock()
	key := previous.keyLocked()
	m := previous.monitor
	if m.runtime != "" {
		m = &sessionMonitor{terminalID: m.terminalID, runtime: monitorRuntimeID(uuid.NewString()), entries: make(map[*monitorEntry]struct{})}
	}
	previous.mu.Unlock()
	entry := &monitorEntry{monitor: m, conversation: observation.NativeID, succeeds: key,
		conversationInput: monitorConversation{agentConversationObservation: observation, Claim: claim, Terminal: terminal}}
	entry.sink = monitorHome{daemon: d, entry: entry}
	entry.input = &sessionInputLane{mu: &entry.mu, entry: entry}
	return entry.sink.Applied(entry.viewLocked())
}

func (d *Daemon) admitMonitorConversation(entry *monitorEntry, view monitorView) bool {
	if view.Succeeds.V == (monitorKey{}) {
		d.logf("monitor apply: unknown entry %+v without predecessor", view.Key.V)
		return false
	}
	r := d.monitors()
	r.mu.Lock()
	var session protocol.SessionID
	for id, binding := range r.sessions {
		binding.entry.mu.Lock()
		matches := binding.entry.keyLocked() == view.Succeeds.V
		binding.entry.mu.Unlock()
		if matches {
			session = id
			break
		}
	}
	r.mu.Unlock()
	if session == "" || view.Conversation.V.NativeID == "" {
		d.logf("monitor apply: unknown entry %+v with predecessor %+v", view.Key.V, view.Succeeds.V)
		return false
	}
	conversation := view.Conversation.V
	observation := conversation.agentConversationObservation
	observation.SessionID = session
	if conversation.Terminal != "" {
		return d.acceptMonitorConversation(conversation.Terminal, observation, entry)
	}
	transition := d.store.TransitionSessionConversation
	if conversation.Claim {
		transition = d.store.ClaimSessionConversation
	}
	return d.applyAgentConversation(observation, transition, entry)
}

package sessionmonitor

import (
	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/harness"
)

func (e *Entry) ProposeConversation(observation ConversationObservation, proposal ConversationProposal, terminal harness.TerminalID) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	key := e.keyLocked()
	m := e.monitor
	if m.runtime != "" {
		m = &Monitor{terminalID: m.terminalID, runtime: RuntimeID(uuid.NewString()), entries: make(map[*Entry]struct{})}
	}
	e.mu.Unlock()
	successor := &Entry{registry: e.registry, monitor: m, conversation: observation.NativeID, succeeds: key,
		conversationInput: Conversation{ConversationObservation: observation, Proposal: proposal, Terminal: terminal}}
	successor.home = e.registry.host.HomeFor(successor)
	successor.input = &lane{mu: &successor.mu, entry: successor}
	return successor.home.Applied(successor.viewLocked())
}

func (e *Entry) SetConversation(observation ConversationObservation) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.conversationInput = Conversation{ConversationObservation: observation}
	e.mu.Unlock()
}

func (e *Entry) QueueConversationWhile(absent func() bool, observation ConversationObservation) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !absent() {
		return false
	}
	e.pendingConversation = &observation
	return true
}

func (e *Entry) TakePendingConversation() (ConversationObservation, bool) {
	if e == nil {
		return ConversationObservation{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	observation := e.pendingConversation
	e.pendingConversation = nil
	if observation == nil {
		return ConversationObservation{}, false
	}
	return *observation, true
}

func (e *Entry) ObserveResumeID(nativeID string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	if e.conversationInput.NativeID == nativeID {
		e.mu.Unlock()
		return true
	}
	e.conversationInput = Conversation{ConversationObservation: ConversationObservation{NativeID: nativeID}}
	view := e.viewLocked()
	e.mu.Unlock()
	return e.home.Applied(view)
}

package sessionmonitor

import (
	"github.com/victorarias/attn/internal/crashpoint"
	"github.com/victorarias/attn/internal/harness"
)

func (e *Entry) ProposeConversation(observation ConversationObservation, proposal ConversationProposal, terminal harness.TerminalID) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	key := e.keyLocked()
	m := e.monitor
	if m.linked {
		m = &Monitor{terminalID: m.terminalID, linked: true, entries: make(map[*Entry]struct{})}
	}
	proposed := Conversation{ConversationObservation: observation, Proposal: proposal, Terminal: terminal}
	switching := terminal != ""
	var view View
	if switching {
		e.pendingSwitch = proposed
		view = e.viewLocked()
	}
	e.mu.Unlock()
	if switching {
		e.publish(view)
		crashpoint.At(CrashSwitchPending)
	}
	successor := &Entry{registry: e.registry, monitor: m, conversation: observation.NativeID, succeeds: key, conversationInput: proposed}
	successor.home = e.registry.host.HomeFor(successor)
	successor.input = &lane{mu: &successor.mu, entry: successor}
	decided := successor.publish(successor.viewLocked())
	if switching {
		e.SettleSwitch(observation.NativeID)
	}
	return decided
}

const CrashSwitchPending = "monitor-switch-pending"

func (e *Entry) PendingSwitch() (Conversation, bool) {
	if e == nil {
		return Conversation{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pendingSwitch, e.pendingSwitch.NativeID != ""
}

func (e *Entry) SettleSwitch(nativeID string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.pendingSwitch.NativeID == "" || e.pendingSwitch.NativeID != nativeID {
		e.mu.Unlock()
		return
	}
	e.pendingSwitch = Conversation{}
	view := e.viewLocked()
	e.mu.Unlock()
	e.publish(view)
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
		e.SettleSwitch(nativeID)
		return true
	}
	e.conversationInput = Conversation{ConversationObservation: ConversationObservation{NativeID: nativeID}}
	e.pendingSwitch = Conversation{ConversationObservation: e.conversationInput.ConversationObservation, Proposal: ConversationReported}
	view := e.viewLocked()
	e.mu.Unlock()
	return e.publish(view)
}

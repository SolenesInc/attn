package sessionmonitor

import (
	"github.com/victorarias/attn/internal/crashpoint"
	"github.com/victorarias/attn/internal/harness"
)

func (m *Monitor) ProposeConversation(observation ConversationObservation, proposal ConversationProposal, terminal harness.TerminalID) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	key := m.keyLocked()
	w := m.watch
	if w.linked {
		w = &TerminalWatch{terminalID: w.terminalID, linked: true, monitors: make(map[*Monitor]struct{})}
	}
	proposed := Conversation{ConversationObservation: observation, Proposal: proposal, Terminal: terminal}
	switching := terminal != ""
	var view View
	if switching {
		m.pendingSwitch = proposed
		view = m.viewLocked()
	}
	m.mu.Unlock()
	if switching {
		m.publish(view)
		crashpoint.At(CrashSwitchPending)
	}
	successor := &Monitor{registry: m.registry, watch: w, conversation: observation.NativeID, succeeds: key, conversationInput: proposed}
	successor.home = m.registry.host.HomeFor(successor)
	successor.input = &lane{mu: &successor.mu, monitor: successor}
	decided := successor.publish(successor.viewLocked())
	if switching {
		m.SettleSwitch(observation.NativeID)
	}
	return decided
}

const CrashSwitchPending = "monitor-switch-pending"

func (m *Monitor) PendingSwitch() (Conversation, bool) {
	if m == nil {
		return Conversation{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pendingSwitch, m.pendingSwitch.NativeID != ""
}

func (m *Monitor) SettleSwitch(nativeID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.pendingSwitch.NativeID == "" || m.pendingSwitch.NativeID != nativeID {
		m.mu.Unlock()
		return
	}
	m.pendingSwitch = Conversation{}
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

func (m *Monitor) SetConversation(observation ConversationObservation) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.conversationInput = Conversation{ConversationObservation: observation}
	m.mu.Unlock()
}

func (m *Monitor) QueueConversationWhile(absent func() bool, observation ConversationObservation) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !absent() {
		return false
	}
	m.pendingConversation = &observation
	return true
}

func (m *Monitor) TakePendingConversation() (ConversationObservation, bool) {
	if m == nil {
		return ConversationObservation{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	observation := m.pendingConversation
	m.pendingConversation = nil
	if observation == nil {
		return ConversationObservation{}, false
	}
	return *observation, true
}

func (m *Monitor) ObserveResumeID(nativeID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if m.conversationInput.NativeID == nativeID {
		m.mu.Unlock()
		m.SettleSwitch(nativeID)
		return true
	}
	m.conversationInput = Conversation{ConversationObservation: ConversationObservation{NativeID: nativeID}}
	m.pendingSwitch = Conversation{ConversationObservation: m.conversationInput.ConversationObservation, Proposal: ConversationReported}
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

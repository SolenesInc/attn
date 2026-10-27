package sessionmonitor

import (
	"crypto/rand"
	"maps"
	"time"

	"github.com/victorarias/attn/internal/crashpoint"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

type pendingSwitch struct {
	Conversation
	candidate *Monitor
	terminal  harness.TerminalID
}

func (m *Monitor) ProposeConversation(observation ConversationObservation, proposal ConversationProposal, terminal harness.TerminalID) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	same := m.conversationInput.ConversationObservation == observation
	inPlace := m.conversation == "" || m.conversation == observation.NativeID
	linked, profile, boundAtStart := m.watch.linked, m.profile, m.session != ""
	inputTerminal := terminal
	if inputTerminal == "" {
		inputTerminal = m.primaryTerminalLocked()
	}
	if inputTerminal == "" {
		inputTerminal = m.watch.terminalID
	}
	m.mu.Unlock()
	if same {
		return true
	}
	r := m.registry
	r.mu.Lock()
	current := r.currentInTerminalLocked(inputTerminal, m)
	r.mu.Unlock()
	if current != m {
		return current.ProposeConversation(observation, proposal, terminal)
	}
	if !inPlace && boundAtStart && !linked {
		m.mu.Lock()
		agent, path := m.agent, m.conversationInput.TranscriptPath
		m.mu.Unlock()
		m.FinishAndSettleUsage(agent, path)()
	}
	proposed := Conversation{ConversationObservation: observation, Proposal: proposal, Terminal: terminal}
	candidate := m
	if !inPlace {
		candidate = m.candidate(proposed)
	}
	r.mu.Lock()
	if !inPlace && !linked && !boundAtStart && m.Session() != "" {
		r.mu.Unlock()
		m.mu.Lock()
		agent, path := m.agent, m.conversationInput.TranscriptPath
		m.mu.Unlock()
		m.FinishAndSettleUsage(agent, path)()
		r.mu.Lock()
	}
	current = r.currentInTerminalLocked(inputTerminal, m)
	if current != m {
		r.mu.Unlock()
		return current.ProposeConversation(observation, proposal, terminal)
	}
	if !inPlace && linked {
		if existing := r.conversationLocked(profile, observation.NativeID); existing != nil {
			candidate = existing
		}
	}
	if candidate != m {
		candidate.TakeUserSubmit(m)
	}
	owner := r.owners[inputTerminal]
	if owner == nil {
		owner = m
	}
	owner.mu.Lock()
	owner.pendingSwitch = append(owner.pendingSwitch, pendingSwitch{Conversation: proposed, candidate: candidate, terminal: inputTerminal})
	owner.pendingDirty = true
	view := owner.viewLocked()
	owner.mu.Unlock()
	r.monitors[candidate] = struct{}{}
	r.owners[inputTerminal] = owner
	r.inputs[inputTerminal] = candidate
	r.mu.Unlock()
	candidate.Arm()
	if !owner.save(view) || !owner.savePending() {
		return false
	}
	crashpoint.At(CrashSwitchPending)
	return owner.publish(view)
}

func (r *Registry) currentInTerminalLocked(terminal harness.TerminalID, fallback *Monitor) *Monitor {
	if current := r.inputs[terminal]; current != nil {
		return current
	}
	shown := protocol.SessionID(r.host.ViewOwner(terminal))
	for candidate := range r.monitors {
		candidate.mu.Lock()
		matches := candidate.session == shown && !candidate.stopped
		candidate.mu.Unlock()
		if shown != "" && matches {
			return candidate
		}
	}
	return fallback
}

func (m *Monitor) candidate(proposed Conversation) *Monitor {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.watch
	if w.linked {
		w = &TerminalWatch{terminalID: w.terminalID, linked: true, monitors: make(map[*Monitor]struct{})}
	}
	now := time.Now()
	candidate := &Monitor{registry: m.registry, watch: w, epoch: rand.Text(), conversation: proposed.NativeID,
		conversationInput: proposed, agent: m.agent, profile: m.profile, link: m.link, startedAt: now, directory: m.directory, branch: m.branch,
		policy: m.policy, initialized: true, driver: m.driver,
		state: StateView{State: protocol.SessionStateIdle, Since: now, Updated: now}}
	if proposed.Terminal != "" && proposed.NativeID != m.conversationInput.NativeID {
		candidate.policy.TitlesAllowed = true
	}
	candidate.home = m.registry.host.HomeFor(candidate)
	candidate.input = candidate.freshLaneLocked()
	candidate.SetTerminals(m.terminalViews().owner, m.terminalViews().terminals)
	return candidate
}

const CrashSwitchPending = "monitor-switch-pending"

func (m *Monitor) pendingHeadLocked() Conversation {
	if len(m.pendingSwitch) == 0 {
		return Conversation{}
	}
	return m.pendingSwitch[0].Conversation
}

func (m *Monitor) pendingChainLocked() []Conversation {
	var chain []Conversation
	for _, pending := range m.pendingSwitch {
		chain = append(chain, pending.Conversation)
	}
	return chain
}

func (m *Monitor) PendingSwitch() (Conversation, bool) {
	if m == nil {
		return Conversation{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pending := m.pendingHeadLocked()
	return pending, pending.NativeID != ""
}

func (r *Registry) Input(terminal harness.TerminalID, fallback *Monitor) *Monitor {
	r.mu.Lock()
	defer r.mu.Unlock()
	if candidate := r.inputs[terminal]; candidate != nil {
		return candidate
	}
	return fallback
}

func (m *Monitor) Candidate(conversation string) *Monitor {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pendingSwitch) == 0 || m.pendingSwitch[0].NativeID != conversation {
		return nil
	}
	return m.pendingSwitch[0].candidate
}

func (m *Monitor) RebuildCandidates() {
	if m == nil {
		return
	}
	r := m.registry
	r.mu.Lock()
	m.mu.Lock()
	for i := range m.pendingSwitch {
		pending := &m.pendingSwitch[i]
		if pending.candidate != nil {
			continue
		}
		proposed := pending.Conversation
		terminal := proposed.Terminal
		if terminal == "" {
			terminal = m.primaryTerminalLocked()
		}
		if terminal == "" {
			terminal = m.watch.terminalID
		}
		linked, profile := m.watch.linked, m.profile
		same := m.conversation == "" || m.conversation == proposed.NativeID
		m.mu.Unlock()
		candidate := m
		if !same {
			candidate = m.candidate(proposed)
		}
		if linked {
			if existing := r.conversationLocked(profile, proposed.NativeID); existing != nil {
				candidate = existing
			}
		}
		m.mu.Lock()
		pending.candidate, pending.terminal = candidate, terminal
		r.monitors[candidate] = struct{}{}
		r.owners[terminal] = m
		r.inputs[terminal] = candidate
	}
	m.mu.Unlock()
	r.mu.Unlock()
}

func (m *Monitor) SettleSwitch(nativeID string, bound *Monitor) bool {
	if m == nil || bound == nil {
		return false
	}
	m.mu.Lock()
	further := false
	if len(m.pendingSwitch) > 0 {
		for _, pending := range m.pendingSwitch[1:] {
			further = further || pending.terminal == m.pendingSwitch[0].terminal
		}
	}
	m.mu.Unlock()
	bound.mu.Lock()
	linked, agent, path := bound.watch.linked, bound.agent, bound.conversationInput.TranscriptPath
	bound.mu.Unlock()
	if further && !linked {
		bound.FinishAndSettleUsage(agent, path)()
	}
	bound.mu.Lock()
	final := bound.viewLocked()
	bound.mu.Unlock()
	if !bound.save(final) {
		return false
	}
	r := m.registry
	r.mu.Lock()
	m.mu.Lock()
	if len(m.pendingSwitch) == 0 || m.pendingSwitch[0].NativeID != nativeID {
		m.mu.Unlock()
		r.mu.Unlock()
		return false
	}
	head := m.pendingSwitch[0]
	var tail, remaining []pendingSwitch
	for _, pending := range m.pendingSwitch[1:] {
		if pending.terminal == head.terminal {
			tail = append(tail, pending)
		} else {
			remaining = append(remaining, pending)
		}
	}
	if bound != m {
		bound.mu.Lock()
	}
	target := append([]pendingSwitch(nil), bound.pendingSwitch...)
	if bound == m {
		target = remaining
	}
	target = append(target, tail...)
	conversations := func(entries []pendingSwitch) []Conversation {
		var result []Conversation
		for _, entry := range entries {
			result = append(result, entry.Conversation)
		}
		return result
	}
	var err error
	if r.saver != nil {
		err = r.saver.MovePending(m.session, bound.session, conversations(remaining), conversations(target))
	}
	if err != nil {
		m.logf("monitor pending handoff: %v", err)
		m.retrySaveLocked()
	} else {
		m.pendingSwitch = remaining
		bound.pendingSwitch = target
		m.pendingDirty, bound.pendingDirty = false, false
		if head.candidate == bound {
			bound.conversationInput = head.Conversation
		}
		delete(r.owners, head.terminal)
		for _, pending := range tail {
			r.owners[pending.terminal] = bound
		}
		if len(tail) == 0 {
			delete(r.inputs, head.terminal)
		}
	}
	next := bound.viewLocked()
	if bound != m {
		bound.mu.Unlock()
	}
	m.mu.Unlock()
	r.mu.Unlock()
	if err != nil {
		return false
	}
	if head.candidate != nil && head.candidate != bound {
		r.discardUnbound(head.candidate)
	}
	bound.publish(next)
	return true
}

func (r *Registry) DropSwitches(terminal harness.TerminalID) {
	r.mu.Lock()
	owner := r.owners[terminal]
	delete(r.owners, terminal)
	delete(r.inputs, terminal)
	var dropped []*Monitor
	if owner != nil {
		owner.mu.Lock()
		var remaining []pendingSwitch
		for _, pending := range owner.pendingSwitch {
			if pending.terminal != terminal {
				remaining = append(remaining, pending)
				continue
			}
			dropped = append(dropped, pending.candidate)
		}
		owner.pendingSwitch = remaining
		owner.pendingDirty = true
		if r.saver != nil {
			if err := r.saver.MovePending(owner.session, owner.session, nil, owner.pendingChainLocked()); err != nil {
				owner.logf("monitor drop pending: %v", err)
				owner.retrySaveLocked()
			} else {
				owner.pendingDirty = false
			}
		}
		owner.mu.Unlock()
	}
	r.mu.Unlock()
	for _, m := range dropped {
		if m != owner {
			r.discardUnbound(m)
		}
	}
	if owner != nil {
		owner.Refresh()
	}
}

func (m *Monitor) SetConversation(observation ConversationObservation) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if len(m.pendingSwitch) == 0 {
		m.conversationInput = Conversation{ConversationObservation: observation}
	}
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
	return m.ProposeConversation(ConversationObservation{NativeID: nativeID}, ConversationReported, "")
}

func (r *Registry) Monitors() []*Monitor { return r.each() }

type LinkTarget struct {
	Session      protocol.SessionID
	Profile      string
	Kind         string
	Conversation ConversationObservation
	Terminal     harness.TerminalID
	Driver       DriverMetadata
}

func (m *Monitor) LinkTarget() LinkTarget {
	m.mu.Lock()
	defer m.mu.Unlock()
	conversation := m.conversationInput.ConversationObservation
	if m.conversation != "" {
		conversation.NativeID = m.conversation
	}
	return LinkTarget{Session: m.session, Profile: m.profile, Kind: m.link, Conversation: conversation, Terminal: m.primaryTerminalLocked(), Driver: m.driver}
}

func (m *Monitor) AdoptDropped(candidate *Monitor) {
	if m == nil || candidate == nil {
		return
	}
	candidate.mu.Lock()
	conversation, state, evidence, lane := candidate.conversationInput, candidate.state, candidate.evidence, *candidate.input
	lane.placed = maps.Clone(lane.placed)
	if lane.run != nil {
		run := *lane.run
		lane.run = &run
	}
	candidate.mu.Unlock()
	m.mu.Lock()
	m.conversation, m.state, m.evidence = conversation.NativeID, state, evidence
	m.input = &lane
	lane.mu, lane.monitor = &m.mu, m
	m.mu.Unlock()
	m.ResumeUsage()
	m.StartTranscriptWatcher(conversation.TranscriptPath)
}

func (m *Monitor) savePending() bool {
	r := m.registry
	if r.saver == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.pendingDirty {
		return true
	}
	if err := r.saver.MovePending(m.session, m.session, nil, m.pendingChainLocked()); err != nil {
		m.logf("monitor save pending: %v", err)
		m.retrySaveLocked()
		return false
	}
	m.pendingDirty = false
	return true
}

func (r *Registry) Conversation(profile, native string) *Monitor {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conversationLocked(profile, native)
}

func (r *Registry) conversationLocked(profile, native string) *Monitor {
	var unbound *Monitor
	for m := range r.monitors {
		m.mu.Lock()
		matches := m.watch.linked && m.profile == profile && m.conversation == native && !m.stopped
		bound := m.session != ""
		m.mu.Unlock()
		if matches {
			if bound {
				return m
			}
			unbound = m
		}
	}
	return unbound
}

func (r *Registry) discardUnbound(candidate *Monitor) {
	r.mu.Lock()
	if candidate.Session() != "" {
		r.mu.Unlock()
		return
	}
	for owner := range r.monitors {
		owner.mu.Lock()
		referenced := false
		for _, pending := range owner.pendingSwitch {
			referenced = referenced || pending.candidate == candidate
		}
		owner.mu.Unlock()
		if referenced {
			r.mu.Unlock()
			return
		}
	}
	r.forgetLocked(candidate)
	r.mu.Unlock()
	candidate.Stop()
}

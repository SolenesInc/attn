package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/sessionstate"
)

func (m *Monitor) ResolveSoon() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.resolveSoonLocked()
	m.mu.Unlock()
}

func (m *Monitor) resolveSoonLocked() {
	m.resolveAt, m.resolvePending = time.Time{}, true
	m.registry.rearm()
}

func (r *Registry) next() (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, m := range r.each() {
		m.mu.Lock()
		if m.resolvePending && (!found || m.resolveAt.Before(earliest)) {
			earliest, found = m.resolveAt, true
		}
		m.mu.Unlock()
	}
	return earliest, found
}

func (r *Registry) Run() {
	timer := time.NewTimer(0)
	timer.Stop()
	defer timer.Stop()
	for {
		if !r.host.Do("runSessionResolver", func() { r.ResolveDue(time.Now()) }) {
			return
		}
		if next, ok := r.next(); ok {
			timer.Reset(time.Until(next))
		} else {
			timer.Stop()
		}
		select {
		case <-r.host.Done():
			return
		case <-r.wake:
		case <-timer.C:
		}
	}
}

func (r *Registry) ResolveDue(now time.Time) {
	for _, m := range r.each() {
		m.mu.Lock()
		due := m.resolvePending && !m.resolveAt.After(now)
		if due {
			m.resolvePending = false
		}
		m.mu.Unlock()
		if !due {
			continue
		}
		at := m.resolve(now)
		m.mu.Lock()
		if !m.resolvePending && !at.IsZero() {
			m.resolveAt, m.resolvePending = at, true
		}
		m.mu.Unlock()
	}
}

func (m *Monitor) resolve(now time.Time) time.Time {
	evidence, _ := m.Evidence()
	m.mu.Lock()
	agent, current := m.agent, m.state.State
	m.mu.Unlock()
	policy := sessionstate.PolicyFor(agent)
	resolution := sessionstate.Resolve(evidence.TerminalEvidence, evidence.ConversationEvidence, policy, now)
	owned := m.publishResolution(current, resolution, sessionstate.DwellFor(resolution.State, evidence.TerminalEvidence, evidence.ConversationEvidence, policy), now)
	if !owned {
		return time.Time{}
	}
	next, _ := sessionstate.NextChange(evidence.TerminalEvidence, evidence.ConversationEvidence, policy, now)
	if dwellEnds := m.dwellDeadline(); dwellEnds.After(now) && (next.IsZero() || dwellEnds.Before(next)) {
		next = dwellEnds
	}
	return next
}

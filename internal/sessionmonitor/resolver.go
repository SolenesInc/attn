package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/sessionstate"
)

func (e *Entry) ResolveSoon() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.resolveSoonLocked()
	e.mu.Unlock()
}

func (e *Entry) resolveSoonLocked() {
	e.resolveAt, e.resolvePending = time.Time{}, true
	e.registry.rearm()
}

func (r *Registry) next() (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, e := range r.each() {
		e.mu.Lock()
		if e.resolvePending && (!found || e.resolveAt.Before(earliest)) {
			earliest, found = e.resolveAt, true
		}
		e.mu.Unlock()
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
	for _, e := range r.each() {
		e.mu.Lock()
		due := e.resolvePending && !e.resolveAt.After(now)
		if due {
			e.resolvePending = false
		}
		e.mu.Unlock()
		if !due {
			continue
		}
		at := e.resolve(now)
		e.mu.Lock()
		if !e.resolvePending && !at.IsZero() {
			e.resolveAt, e.resolvePending = at, true
		}
		e.mu.Unlock()
	}
}

func (e *Entry) resolve(now time.Time) time.Time {
	evidence, _ := e.Evidence()
	e.mu.Lock()
	agent, current := e.agent, e.state.State
	e.mu.Unlock()
	policy := sessionstate.PolicyFor(agent)
	resolution := sessionstate.Resolve(evidence.TerminalEvidence, evidence.ConversationEvidence, policy, now)
	owned := e.publishResolution(current, resolution, sessionstate.DwellFor(resolution.State, evidence.TerminalEvidence, evidence.ConversationEvidence, policy), now)
	if !owned {
		return time.Time{}
	}
	next, _ := sessionstate.NextChange(evidence.TerminalEvidence, evidence.ConversationEvidence, policy, now)
	if dwellEnds := e.dwellDeadline(); dwellEnds.After(now) && (next.IsZero() || dwellEnds.Before(next)) {
		next = dwellEnds
	}
	return next
}

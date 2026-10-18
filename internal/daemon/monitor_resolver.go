package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func (d *Daemon) resolveSoon(sessionID protocol.SessionID) {
	if e := d.monitorEntry(sessionID); e != nil {
		e.mu.Lock()
		e.resolveAt, e.resolvePending = time.Time{}, true
		e.mu.Unlock()
		d.monitors().rearm()
	}
}

func (r *monitorRegistry) next() (time.Time, bool) {
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

func (d *Daemon) runSessionResolver() {
	resolver := d.monitors()
	timer := time.NewTimer(0)
	timer.Stop()
	defer timer.Stop()
	for {
		if !d.life.Do("runSessionResolver", func() { d.resolveDue(time.Now()) }) {
			return
		}
		if next, ok := resolver.next(); ok {
			timer.Reset(time.Until(next))
		} else {
			timer.Stop()
		}
		select {
		case <-d.life.Done():
			return
		case <-resolver.wake:
		case <-timer.C:
		}
	}
}

func (d *Daemon) resolveDue(now time.Time) {
	resolver := d.monitors()
	for sessionID, e := range resolver.each() {
		e.mu.Lock()
		due := e.resolvePending && !e.resolveAt.After(now)
		if due {
			e.resolvePending = false
		}
		e.mu.Unlock()
		if !due {
			continue
		}
		at := d.resolveSession(sessionID, now)
		e.mu.Lock()
		if !e.resolvePending && !at.IsZero() {
			e.resolveAt, e.resolvePending = at, true
		}
		e.mu.Unlock()
	}
}

func (d *Daemon) resolveSession(sessionID protocol.SessionID, now time.Time) time.Time {
	session := d.store.Get(sessionID)
	if session == nil {
		d.forgetSessionTrace(sessionID)
		return time.Time{}
	}
	evidence, ok := d.evidenceSnapshot(sessionID)
	if !ok {
		return time.Time{}
	}
	policy := sessionstate.PolicyFor(session.Agent)
	resolution := sessionstate.Resolve(evidence.TerminalEvidence, evidence.ConversationEvidence, policy, now)
	owned := d.publishResolution(sessionID, session.State, resolution, sessionstate.DwellFor(resolution.State, evidence.TerminalEvidence, evidence.ConversationEvidence, policy), now)
	if d.store.Get(sessionID) == nil {
		d.forgetSessionTrace(sessionID)
		return time.Time{}
	}
	if !owned {
		return time.Time{}
	}
	next, _ := sessionstate.NextChange(evidence.TerminalEvidence, evidence.ConversationEvidence, policy, now)
	if dwellEnds := d.dwellDeadline(sessionID); dwellEnds.After(now) && (next.IsZero() || dwellEnds.Before(next)) {
		next = dwellEnds
	}
	return next
}

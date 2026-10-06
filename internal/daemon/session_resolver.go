package daemon

import (
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

type sessionResolver struct {
	mu   sync.Mutex
	due  map[protocol.SessionID]time.Time
	wake chan struct{}
}

func newSessionResolver() *sessionResolver {
	return &sessionResolver{due: make(map[protocol.SessionID]time.Time), wake: make(chan struct{}, 1)}
}

func (r *sessionResolver) soon(sessionID protocol.SessionID) {
	r.mu.Lock()
	r.due[sessionID] = time.Time{}
	r.mu.Unlock()
	r.rearm()
}

func (r *sessionResolver) rearm() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *sessionResolver) takeDue(now time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for id, at := range r.due {
		if !at.After(now) {
			ids = append(ids, string(id))
			delete(r.due, id)
		}
	}
	return ids
}

func (r *sessionResolver) after(sessionID protocol.SessionID, at time.Time) {
	if at.IsZero() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, pending := r.due[sessionID]; !pending {
		r.due[sessionID] = at
	}
}

func (r *sessionResolver) forget(sessionID protocol.SessionID) {
	r.mu.Lock()
	delete(r.due, sessionID)
	r.mu.Unlock()
	r.rearm()
}

func (r *sessionResolver) next() (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var earliest time.Time
	found := false
	for _, at := range r.due {
		if !found || at.Before(earliest) {
			earliest, found = at, true
		}
	}
	return earliest, found
}

func (d *Daemon) sessionResolver() *sessionResolver {
	d.sessionResolverOnce.Do(func() {
		d.sessionResolverState = newSessionResolver()
	})
	return d.sessionResolverState
}

func (d *Daemon) resolveSoon(sessionID protocol.SessionID) {
	d.sessionResolver().soon(sessionID)
}

func (d *Daemon) runSessionResolver() {
	resolver := d.sessionResolver()
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
	resolver := d.sessionResolver()
	for _, sessionID := range resolver.takeDue(now) {
		resolver.after(protocol.SessionID(sessionID), d.resolveSession(protocol.SessionID(sessionID), now))
	}
}

func (d *Daemon) resolveSession(sessionID protocol.SessionID, now time.Time) time.Time {
	session := d.store.Get(sessionID)
	if session == nil {
		d.forgetSessionTrace(sessionID)
		return time.Time{}
	}
	evidence, ok := d.evidenceTable().snapshot(sessionID)
	if !ok {
		return time.Time{}
	}
	policy := sessionstate.PolicyFor(session.Agent)
	resolution := sessionstate.Resolve(evidence, policy, now)
	owned := d.publishResolution(sessionID, session.State, resolution, sessionstate.DwellFor(resolution.State, evidence, policy), now)
	if d.store.Get(sessionID) == nil {
		d.forgetSessionTrace(sessionID)
		return time.Time{}
	}
	if !owned {
		return time.Time{}
	}
	next, _ := sessionstate.NextChange(evidence, policy, now)
	if dwellEnds := d.dwellGate().deadline(sessionID); dwellEnds.After(now) && (next.IsZero() || dwellEnds.Before(next)) {
		next = dwellEnds
	}
	return next
}

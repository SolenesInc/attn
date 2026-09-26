package daemon

import (
	"sync"
	"time"

	"github.com/victorarias/attn/internal/sessionstate"
)

type sessionResolver struct {
	mu   sync.Mutex
	due  map[string]time.Time
	wake chan struct{}
}

func newSessionResolver() *sessionResolver {
	return &sessionResolver{due: make(map[string]time.Time), wake: make(chan struct{}, 1)}
}

func (r *sessionResolver) soon(sessionID string) {
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
			ids = append(ids, id)
			delete(r.due, id)
		}
	}
	return ids
}

func (r *sessionResolver) after(sessionID string, at time.Time) {
	if at.IsZero() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, pending := r.due[sessionID]; !pending {
		r.due[sessionID] = at
	}
}

func (r *sessionResolver) forget(sessionID string) {
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

func (d *Daemon) resolveSoon(sessionID string) {
	d.sessionResolver().soon(sessionID)
}

func (d *Daemon) runSessionResolver() {
	resolver := d.sessionResolver()
	timer := time.NewTimer(0)
	timer.Stop()
	defer timer.Stop()
	for {
		d.resolveDue(time.Now())
		if next, ok := resolver.next(); ok {
			timer.Reset(time.Until(next))
		} else {
			timer.Stop()
		}
		select {
		case <-d.done:
			return
		case <-resolver.wake:
		case <-timer.C:
		}
	}
}

func (d *Daemon) resolveDue(now time.Time) {
	resolver := d.sessionResolver()
	for _, sessionID := range resolver.takeDue(now) {
		resolver.after(sessionID, d.resolveSession(sessionID, now))
	}
}

func (d *Daemon) resolveSession(sessionID string, now time.Time) time.Time {
	session := d.store.Get(sessionID)
	if session == nil {
		d.forgetSessionTrace(sessionID)
		return time.Time{}
	}
	evidence, ok := d.evidenceTable().snapshot(sessionID)
	if !ok {
		return time.Time{}
	}
	policy := sessionstate.PolicyFor(string(session.Agent))
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

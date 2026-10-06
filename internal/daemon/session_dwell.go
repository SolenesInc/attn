package daemon

import (
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type dwellGate struct {
	mu      sync.Mutex
	pending map[protocol.SessionID]dwellPending
}

type dwellPending struct {
	state protocol.SessionState
	until time.Time
}

func newDwellGate() *dwellGate {
	return &dwellGate{pending: make(map[protocol.SessionID]dwellPending)}
}

func (g *dwellGate) ready(sessionID protocol.SessionID, state protocol.SessionState, dwell time.Duration, now time.Time) bool {
	if g == nil || protocol.TrimID(sessionID) == "" {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if dwell <= 0 {
		delete(g.pending, sessionID)
		return true
	}
	pending, ok := g.pending[sessionID]
	if !ok || pending.state != state {
		g.pending[sessionID] = dwellPending{state: state, until: now.Add(dwell)}
		return false
	}
	if now.Before(pending.until) {
		return false
	}
	delete(g.pending, sessionID)
	return true
}

func (g *dwellGate) deadline(sessionID protocol.SessionID) time.Time {
	if g == nil {
		return time.Time{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pending[sessionID].until
}

func (g *dwellGate) clear(sessionID protocol.SessionID) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.pending, sessionID)
}

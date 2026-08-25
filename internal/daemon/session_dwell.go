package daemon

import (
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type dwellGate struct {
	mu      sync.Mutex
	pending map[string]dwellPending
}

type dwellPending struct {
	state protocol.SessionState
	since time.Time
}

func newDwellGate() *dwellGate {
	return &dwellGate{pending: make(map[string]dwellPending)}
}

func (g *dwellGate) ready(sessionID string, state protocol.SessionState, dwell time.Duration, now time.Time) bool {
	if g == nil || strings.TrimSpace(sessionID) == "" {
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
		g.pending[sessionID] = dwellPending{state: state, since: now}
		return false
	}
	if now.Sub(pending.since) < dwell {
		return false
	}
	delete(g.pending, sessionID)
	return true
}

func (g *dwellGate) waiting(sessionID string) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.pending[sessionID]
	return ok
}

func (g *dwellGate) clear(sessionID string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.pending, sessionID)
}

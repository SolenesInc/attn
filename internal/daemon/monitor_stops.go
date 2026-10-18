package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
	"time"
)

func (e *monitorEntry) classifiedTurnID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.classifiedTurn
}

func (e *monitorEntry) setClassifiedTurnID(turnID string) {
	e.mu.Lock()
	e.classifiedTurn = turnID
	e.mu.Unlock()
}

func (e *monitorEntry) beginClassifyingTurn(turnID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.classifiedTurn == turnID || e.classifyingTurn == turnID {
		return false
	}
	e.classifyingTurn = turnID
	return true
}

func (e *monitorEntry) clearClassifyingTurn() {
	e.mu.Lock()
	e.classifyingTurn = ""
	e.mu.Unlock()
}

func (d *Daemon) markForcedStopClassification(sessionID protocol.SessionID) {
	if e := d.monitorEntry(sessionID); e != nil {
		e.mu.Lock()
		e.forcedStop = time.Now()
		e.mu.Unlock()
	}
}

func (d *Daemon) consumeForcedStopClassification(sessionID protocol.SessionID) bool {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	at := e.forcedStop
	e.forcedStop = time.Time{}
	return !at.IsZero() && time.Since(at) <= forcedStopSuppressTTL
}

func (d *Daemon) clearForcedStopClassification(sessionID protocol.SessionID) {
	if e := d.monitorEntry(sessionID); e != nil {
		e.mu.Lock()
		e.forcedStop = time.Time{}
		e.mu.Unlock()
	}
}

package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
	"time"
)

func (d *Daemon) classifiedTurnID(sessionID protocol.SessionID) string {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.classifiedTurn
}

func (d *Daemon) setClassifiedTurnID(sessionID protocol.SessionID, turnID string) {
	if e := d.monitorEntry(sessionID); e != nil {
		e.mu.Lock()
		e.classifiedTurn = turnID
		e.mu.Unlock()
	}
}

func (d *Daemon) clearClassifiedTurn(sessionID protocol.SessionID) {
	d.setClassifiedTurnID(sessionID, "")
}

func (d *Daemon) beginClassifyingTurn(sessionID protocol.SessionID, turnID string) bool {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.classifiedTurn == turnID || e.classifyingTurn == turnID {
		return false
	}
	e.classifyingTurn = turnID
	return true
}

func (d *Daemon) clearClassifyingTurn(sessionID protocol.SessionID) {
	if e := d.monitorEntry(sessionID); e != nil {
		e.mu.Lock()
		e.classifyingTurn = ""
		e.mu.Unlock()
	}
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

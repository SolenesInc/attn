package sessionmonitor

import "time"

func (e *Entry) classifiedTurnID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.classifiedTurn
}

func (e *Entry) setClassifiedTurnID(turnID string) {
	e.mu.Lock()
	e.classifiedTurn = turnID
	e.mu.Unlock()
}

func (e *Entry) beginClassifyingTurn(turnID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.classifiedTurn == turnID || e.classifyingTurn == turnID {
		return false
	}
	e.classifyingTurn = turnID
	return true
}

func (e *Entry) clearClassifyingTurn() {
	e.mu.Lock()
	e.classifyingTurn = ""
	e.mu.Unlock()
}

const forcedStopSuppressTTL = 30 * time.Second

func (e *Entry) MarkForcedStop() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.forcedStop = time.Now()
	e.mu.Unlock()
}

func (e *Entry) ConsumeForcedStop() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	at := e.forcedStop
	e.forcedStop = time.Time{}
	return !at.IsZero() && time.Since(at) <= forcedStopSuppressTTL
}

func (e *Entry) ClearForcedStop() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.forcedStop = time.Time{}
	e.mu.Unlock()
}

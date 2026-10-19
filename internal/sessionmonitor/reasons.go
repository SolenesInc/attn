package sessionmonitor

import "github.com/victorarias/attn/internal/sessionstate"

func (e *Entry) recordStateReason(resolution sessionstate.Resolution) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	reason := string(resolution.Reason)
	changed := e.reason != reason
	e.reason = reason
	return changed
}

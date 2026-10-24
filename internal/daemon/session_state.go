package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/statetrace"
)

func (d *Daemon) applyState(sessionID protocol.SessionID, change sessionmonitor.Change) bool {
	if d.store == nil {
		return false
	}
	if monitor := d.sessionMonitor(sessionID); monitor != nil {
		return monitor.ApplyState(change)
	}
	d.logf("state update discarded: session=%s state=%s: no monitor runs it", sessionID, change.State)
	d.recordStateObservation(sessionID, sessionmonitor.ChangeObservation(change, statetrace.OutcomeDiscarded, "no_monitor"))
	return false
}

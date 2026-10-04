package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/harness"
)

// lockTerminalEnds is held by whatever changes which of a session's terminals run it, so that exactly
// one leaving terminal finds itself the last. Take it after the session's lifecycle lock.
func (d *Daemon) lockTerminalEnds(sessionID string) (unlock func()) {
	lease := d.terminalEndLocks.lease(sessionID)
	lease.Lock()
	return lease.Unlock
}

// othersRun reports whether a terminal other than t runs the session. A terminal counts until its
// runtime is removed, so an exit not yet handled still counts. Hold lockTerminalEnds.
func (d *Daemon) othersRun(sessionID string, t harness.TerminalID) bool {
	live := d.liveTerminals(context.Background())
	for _, id := range d.terminals().Of(harness.SessionID(sessionID)) {
		if _, running := live[id]; running && id != t {
			return true
		}
	}
	return false
}

// endTerminal is the rule for a terminal leaving its session: while another terminal runs the session, t
// ends alone with its tile. The last one changes nothing here and returns true; its caller ends the session.
func (d *Daemon) endTerminal(sessionID string, t harness.TerminalID) (last bool) {
	defer d.lockTerminalEnds(sessionID)()
	if !d.othersRun(sessionID, t) {
		return true
	}
	if err := d.removePTYSession(t); err != nil {
		d.logf("removing the runtime of terminal %s: %v", t, err)
	}
	desktop, removed, err := d.store.RemoveTerminalTile(string(t))
	if err != nil {
		d.logf("removing the tile of terminal %s: %v", t, err)
	} else if removed {
		d.publishArrangementChanged(desktop.ProfileID)
	}
	return false
}

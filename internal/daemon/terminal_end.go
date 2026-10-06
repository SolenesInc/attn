package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

// Take after the session's lifecycle lock.
func (d *Daemon) lockTerminalEnds(sessionID protocol.SessionID) (unlock func()) {
	lease := d.terminalEndLocks.lease(sessionID)
	lease.Lock()
	return lease.Unlock
}

// Hold lockTerminalEnds.
func (d *Daemon) othersRun(sessionID protocol.SessionID, t harness.TerminalID) bool {
	live := d.liveTerminals(context.Background())
	for _, id := range d.terminals().Of(harness.SessionID(sessionID)) {
		if _, running := live[id]; running && id != t {
			return true
		}
	}
	return false
}

func (d *Daemon) endTerminal(sessionID protocol.SessionID, t harness.TerminalID) (last bool) {
	defer d.lockTerminalEnds(sessionID)()
	if !d.othersRun(sessionID, t) {
		return true
	}
	if err := d.removePTYSession(t); err != nil {
		d.logf("removing the runtime of terminal %s: %v", t, err)
	}
	_, _, err := d.store.RemoveTerminalTile(t)
	if err != nil {
		d.logf("removing the tile of terminal %s: %v", t, err)
	} else if session := d.store.Get(sessionID); session != nil {
		d.publishArrangementChanged(session.ProfileID)
	}
	return false
}

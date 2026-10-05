package daemon

import (
	"context"
	"errors"
	"syscall"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
)

// Take after the session's lifecycle lock.
func (d *Daemon) lockTerminalEnds(sessionID string) (unlock func()) {
	lease := d.terminalEndLocks.lease(sessionID)
	lease.Lock()
	return lease.Unlock
}

// Hold lockTerminalEnds.
func (d *Daemon) othersRun(live map[harness.TerminalID]struct{}, sessionID string, t harness.TerminalID) bool {
	for _, id := range d.terminals().Of(harness.SessionID(sessionID)) {
		if _, running := live[id]; running && id != t {
			return true
		}
	}
	return false
}

func (d *Daemon) endTerminal(sessionID string, t harness.TerminalID) (last bool) {
	defer d.lockTerminalEnds(sessionID)()
	if !d.othersRun(d.liveTerminals(context.Background()), sessionID, t) {
		return true
	}
	d.dropTerminal(t)
	return false
}

// closeTerminal stops terminal t while another terminal runs its session; the last one stays for the
// caller to close with the session.
func (d *Daemon) closeTerminal(sessionID string, t harness.TerminalID) (last bool) {
	lifecycle := d.sessionLifecycleLockFor(sessionID)
	lifecycle.Lock()
	defer lifecycle.Unlock()
	defer d.lockTerminalEnds(sessionID)()
	if !d.othersRun(d.liveTerminals(context.Background()), sessionID, t) {
		return true
	}
	if err := d.ptyBackend.Kill(context.Background(), t, syscall.SIGTERM); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		d.logf("stopping terminal %s: %v", t, err)
	}
	d.dropTerminal(t)
	return false
}

func (d *Daemon) dropTerminal(t harness.TerminalID) {
	if err := d.removePTYSession(t); err != nil {
		d.logf("removing the runtime of terminal %s: %v", t, err)
	}
	desktop, removed, err := d.store.RemoveTerminalTile(string(t))
	if err != nil {
		d.logf("removing the tile of terminal %s: %v", t, err)
	} else if removed {
		d.publishArrangementChanged(desktop.ProfileID)
	}
}

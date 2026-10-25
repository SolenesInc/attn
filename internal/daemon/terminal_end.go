package daemon

import (
	"context"
	"errors"
	"slices"
	"syscall"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

// Take after the session's lifecycle lock.
func (d *Daemon) lockTerminalEnds(sessionID protocol.SessionID) (unlock func()) {
	lease := d.terminalEndLocks.lease(sessionID)
	lease.Lock()
	return lease.Unlock
}

// Hold lockTerminalEnds.
func (d *Daemon) othersRun(sessionID protocol.SessionID, closing ...harness.TerminalID) bool {
	live := d.liveTerminals(context.Background())
	for _, id := range d.terminals().Of(harness.SessionID(sessionID)) {
		if _, running := live[id]; running && !slices.Contains(closing, id) {
			return true
		}
	}
	return false
}

// Hold lockTerminalEnds.
func (d *Daemon) endTerminal(sessionID protocol.SessionID, t harness.TerminalID) (last bool) {
	if !d.othersRun(sessionID, t) {
		return true
	}
	d.dropTerminal(t)
	return false
}

func (d *Daemon) closeTerminal(sessionID protocol.SessionID, t harness.TerminalID) (last bool) {
	lifecycle := d.sessionLifecycleLockFor(sessionID)
	lifecycle.Lock()
	defer lifecycle.Unlock()
	defer d.lockTerminalEnds(sessionID)()
	if !d.othersRun(sessionID, t) {
		return true
	}
	if err := d.killTerminalRuntime(t, syscall.SIGTERM); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		d.logf("stopping terminal %s: %v", t, err)
	}
	d.dropTerminal(t)
	return false
}

func (d *Daemon) dropTerminal(t harness.TerminalID) {
	d.forgetTerminalTitle(t)
	d.linkTerminalDropped(t)
	if err := d.removePTYSession(t); err != nil {
		d.logf("removing the runtime of terminal %s: %v", t, err)
	}
	desktop, removed, err := d.store.RemoveTerminalTile(t)
	if err != nil {
		d.logf("removing the tile of terminal %s: %v", t, err)
	} else if removed {
		d.publishArrangementChanged(desktop.ProfileID)
	}
}

func (d *Daemon) killTerminalRuntime(t harness.TerminalID, sig syscall.Signal) error {
	_, err := Call[struct{}](context.Background(), d.router(), protocol.TerminalKillMessage{Cmd: protocol.CmdTerminalKill, TerminalID: t, Signal: signalName(sig)})
	return err
}

func (d *Daemon) removeTerminalRuntime(ctx context.Context, t harness.TerminalID) error {
	_, err := Call[struct{}](ctx, d.router(), protocol.TerminalRemoveMessage{Cmd: protocol.CmdTerminalRemove, TerminalID: t})
	return err
}

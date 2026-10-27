package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"syscall"

	"github.com/victorarias/attn/internal/bridge"
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
	owner, _ := d.shownIn(t)
	d.forgetTerminalTitle(t)
	d.linkTerminalDropped(t)
	if err := d.removePTYSession(t); err != nil {
		d.logf("removing the runtime of terminal %s: %v", t, err)
	}
	if d.copiedSession(owner) {
		d.life.Go("reportTerminalExit", func() {
			_, err := Call[struct{}](d.life.Context(), d.router(), protocol.TerminalExitedMessage{Cmd: "terminal_exited", SessionID: owner, Running: d.runningSessionTerminals(owner)})
			if err != nil {
				d.logf("reporting exit of terminal %s: %v; bridge recovery will reconcile it", t, err)
			}
		})
		return
	}
	d.removeTerminalTile(t)
}

func (d *Daemon) removeTerminalTile(t harness.TerminalID) {
	desktop, removed, err := d.store.RemoveTerminalTile(t)
	if err != nil {
		d.logf("removing the tile of terminal %s: %v", t, err)
	} else if removed {
		d.publishArrangementChanged(desktop.ProfileID)
	}
}

func (d *Daemon) terminalExited(ctx context.Context, msg protocol.TerminalExitedMessage) (struct{}, error) {
	if origin, remote := OriginOf(ctx); remote && d.sessionHost(msg.SessionID) != origin.Host {
		return struct{}{}, fmt.Errorf("session %s does not run on %s", msg.SessionID, origin.Name)
	}
	return struct{}{}, d.reconcileRemoteTerminals(ctx, msg.SessionID, msg.Running)
}

func (d *Daemon) reconcileRemoteTerminals(ctx context.Context, session protocol.SessionID, running []harness.TerminalID) error {
	lifecycle := d.sessionLifecycleLockFor(session)
	lifecycle.Lock()
	defer lifecycle.Unlock()
	defer d.lockTerminalEnds(session)()
	for _, terminal := range d.terminalsOf(session) {
		if slices.Contains(running, terminal) || len(d.terminalsOf(session)) <= 1 {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, bridge.LostAfter)
		snapshot, err := Call[protocol.GetScreenSnapshotResultMessage](probeCtx, d.router(), protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: terminal})
		cancel()
		if err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
			return err
		}
		if protocol.Deref(snapshot.Running) {
			continue
		}
		d.dropTerminal(terminal)
	}
	return nil
}

func (d *Daemon) killTerminalRuntime(t harness.TerminalID, sig syscall.Signal) error {
	_, err := Call[struct{}](context.Background(), d.router(), protocol.TerminalKillMessage{Cmd: protocol.CmdTerminalKill, TerminalID: t, Signal: signalName(sig)})
	return err
}

func (d *Daemon) removeTerminalRuntime(ctx context.Context, t harness.TerminalID) error {
	_, err := Call[struct{}](ctx, d.router(), protocol.TerminalRemoveMessage{Cmd: protocol.CmdTerminalRemove, TerminalID: t})
	return err
}

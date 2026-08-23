package daemon

import (
	"context"
	"os"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// terminalUpgradeTimeout bounds the in-place worker swap. The measured cost is
// ~10ms end to end (7-8ms blackout plus the re-handshake), so anything near
// this is a worker that stopped answering, not a slow upgrade.
const terminalUpgradeTimeout = 30 * time.Second

// inplaceUpgradeEnvVar turns the swap off. Past the capture the worker has no
// way back — it exits, and the session goes recoverable — so if a bump makes
// the swap misbehave, the way out has to be an env var and a daemon restart,
// not a release. Off falls back to the reload notice, which leaves the agent
// running until the user chooses.
const inplaceUpgradeEnvVar = "ATTN_WORKER_INPLACE_UPGRADE"

func inplaceUpgradeEnabled() bool {
	return os.Getenv(inplaceUpgradeEnvVar) != "0"
}

// handleTerminalBuildChanged runs when a session's worker reports which
// libghostty-vt it holds. When it no longer matches this daemon's, the worker
// replaces its own binary in place and the user sees nothing; the reload notice
// is what happens when that fails.
func (d *Daemon) handleTerminalBuildChanged(sessionID, workerFormat string) {
	upgrader, canUpgrade := d.ptyBackend.(ptybackend.WorkerUpgrader)
	// The worker's answer comes with the call rather than being read back: this
	// can run before the backend has the session in its map.
	if !canUpgrade || !inplaceUpgradeEnabled() || workerFormat == buildinfo.SnapshotFormat {
		d.publishFact(FactSessionTerminalBuildChanged, sessionID, nil)
		return
	}
	// A daemon start handshakes the same worker more than once — the recovery
	// probe and the lifecycle watch each get their own hello, each reporting a
	// format nothing has recorded yet. Without this, both upgrade: one wins and
	// the other is told an upgrade is already running, which used to publish the
	// stale flag and flash a reload notice at a session that had just been
	// swapped.
	if !d.claimWorkerUpgrade(sessionID) {
		return
	}
	d.logf("terminal build: session=%s worker=%q daemon=%s; upgrading in place",
		sessionID, workerFormat, buildinfo.SnapshotFormat)
	// This runs inside the handshake that noticed the mismatch, and upgrading
	// needs a connection of its own.
	go d.upgradeStaleWorker(sessionID, upgrader)
}

// claimWorkerUpgrade reports whether this caller is the one that upgrades the
// session, and marks it in flight when it is.
func (d *Daemon) claimWorkerUpgrade(sessionID string) bool {
	d.upgradingMu.Lock()
	defer d.upgradingMu.Unlock()
	if d.upgradingWorkers[sessionID] {
		return false
	}
	if d.upgradingWorkers == nil {
		d.upgradingWorkers = make(map[string]bool)
	}
	d.upgradingWorkers[sessionID] = true
	return true
}

func (d *Daemon) releaseWorkerUpgrade(sessionID string) {
	d.upgradingMu.Lock()
	defer d.upgradingMu.Unlock()
	delete(d.upgradingWorkers, sessionID)
}

func (d *Daemon) upgradeStaleWorker(sessionID string, upgrader ptybackend.WorkerUpgrader) {
	// Released after the upgrade's own re-handshake has been through
	// handleTerminalBuildChanged, so the claim covers the whole round trip.
	defer d.releaseWorkerUpgrade(sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), terminalUpgradeTimeout)
	defer cancel()
	if err := upgrader.UpgradeWorker(ctx, sessionID); err != nil {
		d.logf("terminal upgrade: session=%s failed within %s (%v); offering a reload instead",
			sessionID, terminalUpgradeTimeout, err)
		d.publishFact(FactSessionTerminalBuildChanged, sessionID, nil)
		return
	}
	// The re-handshake inside the upgrade reports the new build and comes back
	// through here, which is what publishes the session with the flag cleared.
	d.logf("terminal upgrade: session=%s worker swapped in place", sessionID)
}

// decorateSessionWithTerminalBuild flags a session whose pty-worker holds a
// different libghostty-vt than this daemon.
//
// A worker process outlives an install, so updating the app under a running
// session leaves the worker parsing the PTY with the old terminal while the app
// renders with the new one. They stop agreeing about the grid: synthesized
// layout bytes, kitty placements, and OSC 133 block rows are all computed on the
// worker's model and replayed into the app's. The snapshot path already refuses
// mismatched bytes, so a drifted pane cannot even self-heal on resync. The app
// answers by offering a reload, which is what replaces the worker.
//
// The format tag is derived from the two ghostty locks, not from the attn
// version, so an ordinary release leaves it byte-identical and disturbs nothing.
// Only a ghostty bump moves it.
func (d *Daemon) decorateSessionWithTerminalBuild(clone *protocol.Session) {
	if clone == nil {
		return
	}
	clone.TerminalBuildStale = nil
	provider, ok := d.ptyBackend.(ptybackend.TerminalBuildProvider)
	if !ok {
		return
	}
	format, known := provider.SessionTerminalBuild(clone.ID)
	// An unknown answer is not a verdict. An empty format IS one, and that is
	// the point: every worker built before the field existed reports nothing,
	// and those are exactly the sessions the first bump after this strands.
	if known && format != buildinfo.SnapshotFormat {
		clone.TerminalBuildStale = protocol.Ptr(true)
	}
}

package daemon

import (
	"context"
	"os"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// terminalUpgradeTimeout is a tripwire: the swap measures ~10ms end to end, so
// anything near this is a worker that stopped answering.
const terminalUpgradeTimeout = 30 * time.Second

// inplaceUpgradeEnvVar turns the swap off. Past the capture the worker has no way back, so
// the escape hatch has to be an env var and a daemon restart, not a release.
const inplaceUpgradeEnvVar = "ATTN_WORKER_INPLACE_UPGRADE"

func inplaceUpgradeEnabled() bool {
	return os.Getenv(inplaceUpgradeEnvVar) != "0"
}

func (d *Daemon) handleTerminalBuildChanged(sessionID, workerFormat string) {
	upgrader, canUpgrade := d.ptyBackend.(ptybackend.WorkerUpgrader)
	if !canUpgrade || !inplaceUpgradeEnabled() || workerFormat == buildinfo.SnapshotFormat {
		d.publishFact(FactSessionTerminalBuildChanged, sessionID, nil)
		return
	}
	// A daemon start handshakes the same worker more than once (recovery probe, lifecycle
	// watch). Without this claim both upgrade, and the loser publishes a stale flag.
	if !d.claimWorkerUpgrade(sessionID) {
		return
	}
	d.logf("terminal build: session=%s worker=%q daemon=%s; upgrading in place",
		sessionID, workerFormat, buildinfo.SnapshotFormat)
	go d.upgradeStaleWorker(sessionID, upgrader)
}

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
	d.logf("terminal upgrade: session=%s worker swapped in place", sessionID)
}

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
	// An unknown answer is not a verdict; an empty format is one — a worker built
	// before the field existed is exactly what the next bump strands.
	if known && format != buildinfo.SnapshotFormat {
		clone.TerminalBuildStale = protocol.Ptr(true)
	}
}

package daemon

import (
	"context"
	"os"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

const terminalUpgradeTimeout = 30 * time.Second

const inplaceUpgradeEnvVar = "ATTN_WORKER_INPLACE_UPGRADE"

func inplaceUpgradeEnabled() bool {
	return os.Getenv(inplaceUpgradeEnvVar) != "0"
}

func (d *Daemon) handleTerminalBuildChanged(sessionID, workerFormat string) {
	if d.sessionCanReplayTerminalBuild(sessionID) {
		d.publishFact(FactSessionTerminalBuildChanged, sessionID, nil)
		return
	}
	upgrader, canUpgrade := d.ptyBackend.(ptybackend.WorkerUpgrader)
	if !canUpgrade || !inplaceUpgradeEnabled() || workerFormat == buildinfo.SnapshotFormat {
		d.publishFact(FactSessionTerminalBuildChanged, sessionID, nil)
		return
	}
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
	if known && format != buildinfo.SnapshotFormat && !d.sessionCanReplayTerminalBuild(clone.ID) {
		clone.TerminalBuildStale = protocol.Ptr(true)
	}
}

func (d *Daemon) sessionCanReplayTerminalBuild(sessionID string) bool {
	provider, ok := d.ptyBackend.(ptybackend.TerminalBuildCompatibilityProvider)
	return ok && provider.SessionCanReplayWithFormat(sessionID, buildinfo.SnapshotFormat)
}

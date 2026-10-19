package daemon

import (
	"context"
	"os"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

const terminalUpgradeTimeout = 30 * time.Second

const inplaceUpgradeEnvVar = "ATTN_WORKER_INPLACE_UPGRADE"

func inplaceUpgradeEnabled() bool {
	return os.Getenv(inplaceUpgradeEnvVar) != "0"
}

func (d *Daemon) handleTerminalBuildChanged(terminal harness.TerminalID, workerFormat string) {
	d.upgradeInPlace(terminal, workerFormat)
}

func (d *Daemon) upgradeInPlace(terminal harness.TerminalID, workerFormat string) <-chan struct{} {
	sessionID, shown := d.shownIn(terminal)
	if !shown || !d.ptyRecovered.Load() {
		return nil
	}
	upgrader, canUpgrade := d.ptyBackend.(ptybackend.WorkerUpgrader)
	if !canUpgrade || !inplaceUpgradeEnabled() || workerFormat == buildinfo.SnapshotFormat {
		d.publishFact(FactSessionTerminalBuildChanged, string(sessionID), nil)
		return nil
	}
	done, claimed := d.claimWorkerUpgrade(terminal)
	if !claimed {
		return done
	}
	d.logf("terminal build: session=%s terminal=%s worker=%q daemon=%s; upgrading in place",
		sessionID, terminal, workerFormat, buildinfo.SnapshotFormat)
	if !d.life.Go("upgradeStaleWorker", func() { d.upgradeStaleWorker(sessionID, terminal, upgrader) }) {
		d.releaseWorkerUpgrade(terminal)
	}
	return done
}

func (d *Daemon) upgradeStaleTerminals() {
	d.ptyRecovered.Store(true)
	provider, ok := d.ptyBackend.(ptybackend.TerminalBuildProvider)
	if !ok || d.store == nil {
		return
	}
	var upgrades []<-chan struct{}
	for _, session := range d.store.List("") {
		for _, terminal := range d.terminalsOf(session.ID) {
			if format, known := provider.SessionTerminalBuild(terminal); known && format != buildinfo.SnapshotFormat {
				if done := d.upgradeInPlace(terminal, format); done != nil {
					upgrades = append(upgrades, done)
				}
			}
		}
	}
	for _, done := range upgrades {
		<-done
	}
}

func (d *Daemon) claimWorkerUpgrade(terminal harness.TerminalID) (<-chan struct{}, bool) {
	d.upgradingMu.Lock()
	defer d.upgradingMu.Unlock()
	if running, ok := d.upgradingWorkers[terminal]; ok {
		return running, false
	}
	if d.upgradingWorkers == nil {
		d.upgradingWorkers = make(map[harness.TerminalID]chan struct{})
	}
	done := make(chan struct{})
	d.upgradingWorkers[terminal] = done
	return done, true
}

func (d *Daemon) workerUpgradeRunning(terminal harness.TerminalID) bool {
	d.upgradingMu.Lock()
	defer d.upgradingMu.Unlock()
	_, running := d.upgradingWorkers[terminal]
	return running
}

func (d *Daemon) releaseWorkerUpgrade(terminal harness.TerminalID) {
	d.upgradingMu.Lock()
	defer d.upgradingMu.Unlock()
	close(d.upgradingWorkers[terminal])
	delete(d.upgradingWorkers, terminal)
}

func (d *Daemon) upgradeStaleWorker(sessionID protocol.SessionID, terminal harness.TerminalID, upgrader ptybackend.WorkerUpgrader) {
	ctx, cancel := context.WithTimeout(context.Background(), terminalUpgradeTimeout)
	defer cancel()
	err := upgrader.UpgradeWorker(ctx, terminal)
	d.releaseWorkerUpgrade(terminal)
	if err != nil {
		d.logf("terminal upgrade: session=%s failed within %s (%v); offering a reload instead",
			sessionID, terminalUpgradeTimeout, err)
		d.publishFact(FactSessionTerminalBuildChanged, string(sessionID), nil)
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
	terminal := d.primaryTerminal(clone.ID)
	format, known := provider.SessionTerminalBuild(terminal)
	if known && format != buildinfo.SnapshotFormat && d.ptyRecovered.Load() && !d.workerUpgradeRunning(terminal) {
		clone.TerminalBuildStale = protocol.Ptr(true)
	}
}

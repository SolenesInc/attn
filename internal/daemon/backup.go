package daemon

import (
	"os"
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) backupDir() string {
	return filepath.Join(d.dataRoot, "backups")
}

const backupTimer stepclock.Name = "daemon-backup"

func (d *Daemon) runDatabaseBackupLoop() {
	d.performDatabaseBackup()
	d.every(backupTimer, backupInterval, d.performDatabaseBackup)
}

func (d *Daemon) every(name stepclock.Name, interval time.Duration, tick func()) {
	timer := d.clock.NewTimer(name, interval)
	defer timer.Stop()
	for {
		select {
		case <-d.life.Done():
			return
		case <-timer.C():
			tick()
			timer.Reset(interval)
		}
	}
}

func (d *Daemon) startPermanentMaintenance() {
	d.life.Go("runDatabaseBackupLoop", d.runDatabaseBackupLoop)
	d.life.Go("reportLoudProducersAtStart", d.eventBus.ReportLoudProducers)
}

func (d *Daemon) performDatabaseBackup() {
	defer func() {
		if r := recover(); r != nil {
			d.logf("database backup panicked: %v", r)
		}
	}()

	if d.store == nil {
		return
	}

	pausepoint.At(pausepoint.DatabaseBackupStarting)
	path, err := d.store.BackupNow(d.life.Context(), d.backupDir())
	pausepoint.Done(pausepoint.DatabaseBackupCopied, err)
	if err != nil {
		d.logf("database backup failed: %v", err)
		return
	}
	d.logf("database backup written to %s", path)

	d.lastBackupMu.Lock()
	d.lastBackupAt = d.clock.Now().UTC()
	d.lastBackupMu.Unlock()

	d.publishFact(FactBackupWritten, path, nil)
	d.pruneEligibleDatabaseBackups()
}

func (d *Daemon) pruneEligibleDatabaseBackups() {
	d.pruneDatabaseBackups(nil)
}

func (d *Daemon) pruneDatabaseBackups(protected map[string]struct{}) {
	if err := store.PruneBackups(d.backupDir(), backupKeep, protected); err != nil && !os.IsNotExist(err) {
		d.logf("database backup prune: %v", err)
	}
	if d.store == nil || d.store.DatabasePath() == "" {
		return
	}
	premigrationDir := store.BackupDirForDatabase(d.store.DatabasePath())
	if err := store.PrunePremigrationBackups(premigrationDir, store.PremigrationBackupKeep(), protected); err != nil && !os.IsNotExist(err) {
		d.logf("pre-migration backup prune: %v", err)
	}
}

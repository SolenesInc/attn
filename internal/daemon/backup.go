package daemon

import (
	"os"
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) backupDir() string {
	return filepath.Join(d.dataRoot, "backups")
}

func (d *Daemon) runDatabaseBackupLoop() {
	d.performDatabaseBackup()

	ticker := time.NewTicker(backupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.life.Done():
			return
		case <-ticker.C:
			d.performDatabaseBackup()
		}
	}
}

func (d *Daemon) startPermanentMaintenance() {
	d.life.Go("runDatabaseBackupLoop", d.runDatabaseBackupLoop)
	d.life.Go("runAutomationRetentionSweep", d.runAutomationRetentionSweep)
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

	path, err := d.store.BackupNow(d.life.Context(), d.backupDir())
	if err != nil {
		d.logf("database backup failed: %v", err)
		return
	}
	d.logf("database backup written to %s", path)

	d.lastBackupMu.Lock()
	d.lastBackupAt = time.Now().UTC()
	d.lastBackupMu.Unlock()

	d.publishSettingsFact(FactBackupWritten, path)
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

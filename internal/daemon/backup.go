package daemon

import (
	"path/filepath"
	"time"
)

// Read off the daemon instance rather than the global config so daemon tests,
// whose dataRoot is a throwaway temp dir, never write into the real ~/.attn.
func (d *Daemon) backupDir() string {
	return filepath.Join(d.dataRoot, "backups")
}

func (d *Daemon) runDatabaseBackupLoop() {
	d.performDatabaseBackup()

	ticker := time.NewTicker(backupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.performDatabaseBackup()
		}
	}
}

// Recovers from a panic in the store backup path so a corrupt or wedged database
// cannot take the daemon down with it.
func (d *Daemon) performDatabaseBackup() {
	defer func() {
		if r := recover(); r != nil {
			d.logf("database backup panicked: %v", r)
		}
	}()

	if d.store == nil {
		return
	}

	path, err := d.store.BackupNow(d.backupDir())
	if err != nil {
		d.logf("database backup failed: %v", err)
		return
	}
	d.logf("database backup written to %s", path)

	d.lastBackupMu.Lock()
	d.lastBackupAt = time.Now().UTC()
	d.lastBackupMu.Unlock()

	d.publishSettingsFact(FactBackupWritten, path)
	d.pruneLegacyTicketRecoveryBackups()
}

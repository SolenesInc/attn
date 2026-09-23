package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/store"
)

const MigrationFailureFileName = "migration-failure.json"

type MigrationFailure struct {
	DaemonLogPath     string `json:"daemon_log_path"`
	DataDir           string `json:"data_dir"`
	DatabasePath      string `json:"database_path"`
	BackupPath        string `json:"backup_path"`
	SchemaVersionFrom int    `json:"schema_version_from"`
	SchemaVersionTo   int    `json:"schema_version_to"`
	Error             string `json:"error"`
	BinaryVersion     string `json:"binary_version"`
	BinaryCommit      string `json:"binary_commit"`
	FailedAt          string `json:"failed_at"`
}

func MigrationFailurePath(dataRoot string) string {
	return filepath.Join(dataRoot, MigrationFailureFileName)
}

func (d *Daemon) openStore() error {
	if d.store != nil {
		return nil
	}
	dbPath := config.DBPath()
	opened, upgrade, err := store.Open(dbPath)
	markerPath := MigrationFailurePath(d.dataRoot)
	if err != nil {
		failure := MigrationFailure{
			DaemonLogPath:     config.LogPath(),
			DataDir:           d.dataRoot,
			DatabasePath:      dbPath,
			BackupPath:        upgrade.BackupPath,
			SchemaVersionFrom: upgrade.From,
			SchemaVersionTo:   upgrade.To,
			Error:             err.Error(),
			BinaryVersion:     buildinfo.Version,
			BinaryCommit:      buildinfo.GitCommit,
			FailedAt:          time.Now().UTC().Format(time.RFC3339),
		}
		d.logf("database upgrade failed: %v", err)
		if writeErr := writeMigrationFailure(markerPath, failure); writeErr != nil {
			d.logf("could not write the migration failure marker %s: %v", markerPath, writeErr)
			return fmt.Errorf("opening the database at %s: %w (the failure marker could not be written: %v)", dbPath, err, writeErr)
		}
		return fmt.Errorf("opening the database at %s: %w (details in %s)", dbPath, err, markerPath)
	}
	d.store = opened
	if upgrade.From != upgrade.To {
		d.logf("database %s upgraded from schema v%d to v%d; backup %s", dbPath, upgrade.From, upgrade.To, upgrade.BackupPath)
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		d.logf("could not remove the stale migration failure marker %s: %v", markerPath, err)
	}
	return nil
}

func writeMigrationFailure(path string, failure MigrationFailure) error {
	data, err := json.MarshalIndent(failure, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

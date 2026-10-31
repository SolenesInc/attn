package daemon_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheAppsSettingsShowWhenTheDaemonBackedUpOnStart(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		startedAt := time.Now().UTC()
		w.advance(0)
		shown, _ := w.App().Initial.Settings["db.last_backup_at"].(string)
		if backedUpAt, err := time.Parse(time.RFC3339, shown); err != nil || !backedUpAt.Equal(startedAt.Truncate(time.Second)) {
			t.Fatalf("the app's settings show the last backup at %q (%v), want the daemon's start %s", shown, err, startedAt.Format(time.RFC3339))
		}
	})
}

func TestBackupsTakenWithinOneSecondKeepTheLaterStateThroughRotation(t *testing.T) {
	var backups string
	var earlier []string
	preparedBackupUpgrade(t, func(dir string) {
		backups = filepath.Join(dir, "backups")
		earlier = append(earlier, plantBackup(t, backups, "attn-20000101-000000.db"))
		for n := 2; n <= backupRotationKeep; n++ {
			earlier = append(earlier, plantBackup(t, backups, fmt.Sprintf("attn-20000101-000000-%d.db", n)))
		}
	}, func(t *testing.T, w *world) {
		takenSince := func(known ...string) []string {
			return slices.DeleteFunc(backupsNamed(t, backups, "attn-2000"), func(name string) bool {
				return slices.Contains(earlier, name) || slices.Contains(known, name)
			})
		}
		w.advance(0)
		first := takenSince()
		if len(first) != 1 {
			t.Fatalf("after starting within the second that already holds %d backups, the daemon has taken %v, want one", len(earlier), first)
		}

		setSetting(t, w.App(), "capture.shortcut", "the later shortcut")
		w.restart()
		w.advance(0)

		later := takenSince(first[0])
		if len(later) != 1 {
			t.Fatalf("after a restart within the same second, the backups are %v, want the one taken after the restart beside %s", backupsNamed(t, backups, "attn-2000"), first[0])
		}
		if kept := backupsNamed(t, backups, "attn-2000"); len(kept) != backupRotationKeep || !slices.Contains(kept, first[0]) {
			t.Errorf("the rotation kept %v, want %d backups including %s", kept, backupRotationKeep, first[0])
		}
		if got := backedUpSetting(t, filepath.Join(backups, first[0]), "capture.shortcut"); got != "" {
			t.Errorf("the backup taken at the first start holds capture.shortcut %q, want the state from before the change", got)
		}
		if got := backedUpSetting(t, filepath.Join(backups, later[0]), "capture.shortcut"); got != "the later shortcut" {
			t.Errorf("the backup taken after the restart holds capture.shortcut %q, want the changed value", got)
		}
	})
}

func backedUpSetting(t *testing.T, path, key string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil && err != sql.ErrNoRows {
		t.Fatalf("read %s from the backup %s: %v", key, path, err)
	}
	return value
}

func TestReadsKeepWorkingAfterABackupWhileTheDatabaseRefusesWrites(t *testing.T) {
	points := []string{pausepoint.DatabaseBackupStarting, pausepoint.DatabaseBackupCopied}
	inBubbleHeldAt(t, points, func(t *testing.T, w *world, held map[string]*testworld.Pause) {
		cli := w.Client()
		starting, copied := held[pausepoint.DatabaseBackupStarting], held[pausepoint.DatabaseBackupCopied]
		starting.Await()
		synctest.Wait()
		starting.Release()
		copied.Await()
		synctest.Wait()

		restore := w.RefuseDatabaseCommits()
		defer restore()
		if _, err := cli.AutomationDefinitions(); err != nil {
			t.Fatalf("listing automations after a backup while the database refuses writes: %v, want the list", err)
		}
		restore()
		copied.Release()
		synctest.Wait()
	})
}

package daemon_test

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"
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

func TestADaemonRestartedWithinTheSameSecondBacksUpTheStateItRestartedWith(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		backups := filepath.Join(w.Dir, "backups")
		w.advance(0)
		first := backupsNamed(t, backups, "attn-2000")
		if len(first) != 1 {
			t.Fatalf("after starting, the daemon has taken backups %v, want one", first)
		}

		setSetting(t, w.App(), "capture.shortcut", "the later shortcut")
		w.restart()
		w.advance(0)

		taken := backupsNamed(t, backups, "attn-2000")
		if len(taken) != 2 || !slices.Contains(taken, first[0]) {
			t.Fatalf("after a restart within the same second, the backups are %v, want %s and one more", taken, first[0])
		}
		later := taken[0]
		if later == first[0] {
			later = taken[1]
		}
		if got := backedUpSetting(t, filepath.Join(backups, first[0]), "capture.shortcut"); got != "" {
			t.Errorf("the first backup holds capture.shortcut %q, want the state from before the change", got)
		}
		if got := backedUpSetting(t, filepath.Join(backups, later), "capture.shortcut"); got != "the later shortcut" {
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

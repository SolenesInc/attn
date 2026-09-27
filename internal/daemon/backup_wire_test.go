package daemon_test

import (
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

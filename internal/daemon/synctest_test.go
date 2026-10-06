package daemon

import (
	"path/filepath"
	"testing"
)

func newBubbleDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

func stopDaemonBackground(t *testing.T, d *Daemon) {
	t.Helper()
	t.Cleanup(func() {
		d.sessionInputs().stopRetries()
		d.stopAllTranscriptWatchers()
		d.stopInbox()
		d.stopAutoSettleTimers()
		d.stopNotebookWatcher()
		d.stopFsWatchers()
		d.pluginDriverSilence().stop()
	})
}

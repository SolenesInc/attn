package daemon

import (
	"testing"
)

func stopDaemonBackground(t *testing.T, d *Daemon) {
	t.Helper()
	t.Cleanup(func() {
		d.sessionInputs().stopRetries()
		d.stopAllTranscriptWatchers()
		d.stopInbox()
		d.stopAutoSettleTimers()
		d.stopNotebookWatcher()
		d.stopFsWatchers()
		d.stopPluginDriverSilence()
	})
}

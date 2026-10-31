package daemon

import (
	"testing"
)

func stopDaemonBackground(t *testing.T, d *Daemon) {
	t.Helper()
	t.Cleanup(func() {
		d.monitors().registry.Close()
		d.monitors().registry.StopTranscriptWatchers()
		d.stopInbox()
		d.stopAutoSettleTimers()
		d.stopRootWatchers()
	})
}

package daemon

import (
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
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
		d.stopNudgeCountdowns()
		d.stopAgentMailboxDoorbells()
		d.stopAutoSettleTimers()
		d.stopNotebookWatcher()
		d.stopFsWatchers()
		d.pluginDriverSilence().stop()
	})
}

func requireDone(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	synctest.Wait()
	select {
	case <-done:
	default:
		t.Fatal(what)
	}
}

func settleStopClassification(t *testing.T) {
	t.Helper()
	time.Sleep(4 * time.Second)
	synctest.Wait()
}

func newTraceDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
}

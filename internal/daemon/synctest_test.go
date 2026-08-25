package daemon

import (
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// Bubble rules: build the daemon outside, stop subsystems and seed timestamps inside
// (clock starts 2000-01-01), drain every goroutine, and let no real fd in.

// Call ABOVE synctest.Test with the outer T: store.New's goroutine reads as a deadlock inside.
func newBubbleDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

// Call INSIDE the bubble with the bubble T — rule 2. Mirrors Daemon.Stop
// subsystem list, minus what only a started daemon owns.
func stopDaemonBackground(t *testing.T, d *Daemon) {
	t.Helper()
	t.Cleanup(func() {
		d.stopAllTranscriptWatchers()
		d.stopNudgeCountdowns()
		d.stopAutoSettleTimers()
		d.stopSnoozeTimers()
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

func requireNoOutbound(t *testing.T, client *wsClient, what string) {
	t.Helper()
	synctest.Wait()
	select {
	case outbound := <-client.send:
		t.Fatalf("%s: %s", what, string(outbound.payload))
	default:
	}
}

func requireOutbound(t *testing.T, client *wsClient, what string) outboundMessage {
	t.Helper()
	synctest.Wait()
	select {
	case outbound := <-client.send:
		return outbound
	default:
		t.Fatal(what)
		return outboundMessage{}
	}
}

// handleStop's goroutine retries for claudeTranscriptRetryWindow (2s) and is still
// sleeping when the body returns; sleeping past its window retires it.
func settleStopClassification(t *testing.T) {
	t.Helper()
	time.Sleep(4 * time.Second)
	synctest.Wait()
}

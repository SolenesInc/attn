package daemon

import (
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func waitForResolvedState(t *testing.T, d *Daemon, sessionID string, want protocol.SessionState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last protocol.SessionState
	for time.Now().Before(deadline) {
		if session := d.store.Get(sessionID); session != nil {
			last = session.State
			if last == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session %s state = %s, want %s", sessionID, last, want)
}

func writeLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func advancePolls(n int) {
	time.Sleep(time.Duration(n) * transcriptPollInterval)
	synctest.Wait()
}

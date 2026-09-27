package daemon

import (
	"testing"
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

package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func registerSessionForPRTest(t *testing.T, d *Daemon, id string) {
	t.Helper()
	injectTestSession(t, d, protocol.Session{ID: protocol.SessionID(id), Label: id, Directory: t.TempDir(), Agent: protocol.SessionAgentClaude})
}

func newPRDaemonForTest(t *testing.T, sessionID string) *Daemon {
	t.Helper()
	d := newDaemonForTest(t)
	stopDaemonBackground(t, d)
	registerSessionForPRTest(t, d, sessionID)
	return d
}

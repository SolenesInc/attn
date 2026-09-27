package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

const characterizationOldTimestamp = "2000-01-01T00:00:00Z"

func addCharacterizationSession(
	t *testing.T,
	d *Daemon,
	id string,
	agent protocol.SessionAgent,
	state protocol.SessionState,
) {
	t.Helper()
	directory := t.TempDir()
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          agent,
		Directory:      directory,
		ProfileID:      defaultProfileID(t, d.store),
		State:          state,
		StateSince:     characterizationOldTimestamp,
		StateUpdatedAt: characterizationOldTimestamp,
		LastSeen:       characterizationOldTimestamp,
	})
}

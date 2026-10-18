package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func TestDaemon_LateSpawnCannotRecreateClosingSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "late-spawn", Label: "closing", Agent: protocol.SessionAgentCodex, ProfileID: defaultProfileID(t, d.store),
		Directory: t.TempDir(), State: protocol.SessionStateWorking,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if _, err := d.prepareSessionTeardown("late-spawn"); err != nil {
		t.Fatalf("prepare close: %v", err)
	}
	d.commitSessionUnregister("late-spawn", store.SessionClose{By: who.User()})
	client := spawnTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: "late-spawn", Cwd: t.TempDir(), Agent: protocol.AgentShellValue,
		ProfileID: defaultProfileID(t, d.store), Cols: 80, Rows: 24,
	})
	expectSpawnResult(t, client, "late-spawn", false)
	if d.store.Get("late-spawn") != nil || !d.store.SessionCloseIntentional("late-spawn") {
		t.Fatal("late spawn recreated the session or cleared its tombstone")
	}
}

package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
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
	d.commitSessionUnregister("late-spawn", store.SessionClose{By: store.SessionClosedByUser})
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

func TestDaemon_HandleUnregisterWS_KeepsTheOtherAgentOnItsDesktop(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now().UTC().Format(time.RFC3339)
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sess-primary", "sess-next"} {
		d.store.Add(&protocol.Session{
			ID: protocol.SessionID(id), Label: id, Directory: t.TempDir(), ProfileID: profile.ID,
			State: protocol.StateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
		})
		placeTestSession(t, d, id, profile.CurrentDesktopID)
	}

	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[protocol.TerminalID]ptybackend.Stream),
	}
	d.wsHub.clients[client] = true

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{ID: "sess-primary"})

	if got := d.store.Get("sess-primary"); got != nil {
		t.Fatalf("closed session still exists: %+v", got)
	}
	if got := d.store.Get("sess-next"); got == nil {
		t.Fatal("the other session was removed")
	}
	desktop, err := d.store.GetDesktop(profile.CurrentDesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if len(desktop.Panes) != 1 || desktop.Panes[0].SessionID != "sess-next" || desktop.ActivePaneID != desktop.Panes[0].PaneID {
		t.Fatalf("desktop after close = %+v, want the other agent kept and active", desktop)
	}
}

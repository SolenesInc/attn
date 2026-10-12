package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
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

func TestDaemon_ReconcileSessionsWithWorkerBackend_ReapUnplacesTheSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "stale-session", Label: "stale", Agent: protocol.SessionAgentCodex, Directory: "/tmp/stale",
		ProfileID: defaultProfileID(t, d.store),
		State:     protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}
	placeTestSession(t, d, "stale-session", profile.CurrentDesktopID)
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: nil,
		info:    map[string]ptybackend.SessionInfo{},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})

	if report.Reaped != 1 {
		t.Fatalf("reaped = %d, want 1", report.Reaped)
	}
	desktop, err := d.store.GetDesktop(profile.CurrentDesktopID)
	if err != nil || len(desktop.Panes) != 0 {
		t.Fatalf("desktop after reap = %+v err=%v, want the pane gone and the desktop kept", desktop, err)
	}
}

func TestDaemon_PruneSessionsWithoutPTY_RemovesTheReapedPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := "codex-stale"
	desktopID := addStalePlacedSession(t, d, sessionID)

	if removed := d.pruneSessionsWithoutPTY(d.storedSessionIDs(), time.Time{}); removed != 1 {
		t.Fatalf("pruneSessionsWithoutPTY removed = %d, want 1", removed)
	}
	if got := d.store.Get(protocol.SessionID(sessionID)); got != nil {
		t.Fatalf("store.Get(%q) = %+v, want nil", sessionID, got)
	}
	if desktop, err := d.store.GetDesktop(desktopID); err != nil || len(desktop.Panes) != 0 {
		t.Fatalf("desktop after prune = %+v err=%v, want no panes", desktop, err)
	}
}

func addStalePlacedSession(t *testing.T, d *Daemon, sessionID string) string {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: protocol.SessionID(sessionID), Label: sessionID, Agent: protocol.SessionAgentCodex, Directory: "/tmp/stale",
		ProfileID: defaultProfileID(t, d.store),
		State:     protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}
	placeTestSession(t, d, sessionID, profile.CurrentDesktopID)
	return profile.CurrentDesktopID
}

func TestDaemon_PruneSessionsWithoutPTY_KeepsTheTilesOfItsDesktop(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := "codex-stale-tile"
	desktopID := addStalePlacedSession(t, d, sessionID)
	desktop, err := d.store.GetDesktop(desktopID)
	if err != nil {
		t.Fatal(err)
	}
	tileID := markdownTileIDForPath("/tmp/notes.md")
	if _, err := d.store.EditDesktopArrangement(desktopID, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		next, ok := layouttree.DockTile(desktop.Tree, desktop.Panes[0].PaneID, layouttree.DirectionVertical, false, "split-tile", tileID, string(layouttree.TileKindMarkdown), "/tmp/notes.md", "", layouttree.DefaultSplitRatio)
		if !ok {
			t.Fatal("dock the markdown tile")
		}
		desktop.Tree = next
		return desktop, nil
	}); err != nil {
		t.Fatal(err)
	}

	if removed := d.pruneSessionsWithoutPTY(d.storedSessionIDs(), time.Time{}); removed != 1 {
		t.Fatalf("pruneSessionsWithoutPTY removed = %d, want 1", removed)
	}
	desktop, err = d.store.GetDesktop(desktopID)
	if err != nil {
		t.Fatal(err)
	}
	if tiles := layouttree.TileIDs(desktop.Tree); len(desktop.Panes) != 0 || len(tiles) != 1 || tiles[0] != tileID {
		t.Fatalf("desktop after prune = panes %+v tiles %v, want only the markdown tile", desktop.Panes, tiles)
	}
}

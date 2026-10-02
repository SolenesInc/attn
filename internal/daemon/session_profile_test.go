package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func defaultProfileID(t testing.TB, s *store.Store) string {
	t.Helper()
	profile, err := s.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatalf("read the default profile: %v", err)
	}
	return profile.ID
}

func registerTestSession(socketPath, id, label, dir string) error {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(protocol.InjectTestSessionMessage{
		Cmd: protocol.CmdInjectTestSession,
		Session: protocol.Session{
			ID: id, Label: label, Directory: dir, Agent: protocol.SessionAgentClaude,
			State: protocol.SessionStateLaunching,
		},
	}); err != nil {
		return err
	}
	var response protocol.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return err
	}
	if !response.Ok {
		return fmt.Errorf("register test session %s: %s", id, protocol.Deref(response.Error))
	}
	return nil
}

func createTestProfile(t testing.TB, s *store.Store, name string) profiles.Profile {
	t.Helper()
	profile, _, err := s.CreateProfile(name)
	if err != nil {
		t.Fatalf("create profile %s: %v", name, err)
	}
	return profile
}

func deleteTestProfile(t testing.TB, s *store.Store, profileID, destinationID string) {
	t.Helper()
	profile, err := s.GetProfile(profileID)
	if err != nil {
		t.Fatalf("read profile %s: %v", profileID, err)
	}
	if _, err := s.DeleteProfile(profileID, profile.Revision, destinationID); err != nil {
		t.Fatalf("delete profile %s: %v", profileID, err)
	}
}

func placeTestSession(t testing.TB, d *Daemon, sessionID, desktopID string) profiles.Desktop {
	t.Helper()
	desktop, _, err := d.store.PlaceLaunchedSession(store.SessionPlacementRequest{
		DesktopID: desktopID, SessionID: sessionID, Status: profiles.PaneStatusReady,
	})
	if err != nil {
		t.Fatalf("place %s on desktop %q: %v", sessionID, desktopID, err)
	}
	return desktop
}

func focusTestAgent(t testing.TB, d *Daemon, sessionID string) {
	t.Helper()
	placement, placed, err := d.store.SessionPlacement(sessionID)
	if err != nil {
		t.Fatalf("read the placement of %s: %v", sessionID, err)
	}
	if !placed {
		profile, err := d.store.MostRecentlyUsedProfile()
		if err != nil {
			t.Fatalf("read the most recent profile: %v", err)
		}
		if profileID, _ := d.store.SessionProfileID(sessionID); profileID == "" {
			if err := d.store.AssignSessionProfile(sessionID, profile.ID); err != nil {
				t.Fatalf("give %s a profile: %v", sessionID, err)
			}
		}
		placeTestSession(t, d, sessionID, profile.CurrentDesktopID)
		if placement, _, err = d.store.SessionPlacement(sessionID); err != nil {
			t.Fatalf("read the placement of %s: %v", sessionID, err)
		}
	}
	profile, _, err := d.store.SetActivePane(placement.DesktopID, placement.PaneID)
	if err != nil {
		t.Fatalf("focus %s: %v", sessionID, err)
	}
	if _, err := d.store.SelectProfile(profile.ID); err != nil {
		t.Fatalf("select profile %s: %v", profile.ID, err)
	}
	d.publishArrangementChanged(profile.ID)
}

func injectTestSession(t testing.TB, d *Daemon, session protocol.Session) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	go func() {
		d.handleInjectTestSession(serverConn, &protocol.InjectTestSessionMessage{Cmd: protocol.CmdInjectTestSession, Session: session})
		_ = serverConn.Close()
	}()
	var response protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&response); err != nil {
		t.Fatalf("decode inject response: %v", err)
	}
	if !response.Ok {
		t.Fatalf("inject %s: %s", session.ID, protocol.Deref(response.Error))
	}
}

func TestDeletingAProfileAnnouncesEveryMovedAgent(t *testing.T) {
	w := newProfilesTestDaemon(t)
	client, _ := w.connect("")
	doomed := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "doomed"}).Profile
	kept := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "kept"}).Profile
	w.agent("mover", doomed.ID)
	var mu sync.Mutex
	var seen []string
	found := false
	w.d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, event.Event)
		if event.Event == protocol.EventSessionStateChanged && event.Session != nil && event.Session.ID == "mover" && event.Session.ProfileID == kept.ID {
			found = true
		}
	}

	w.mustSend(client, map[string]any{
		"cmd": protocol.CmdProfileDelete, "profile_id": doomed.ID, "expected_revision": doomed.Revision, "destination_profile_id": kept.ID,
	})
	mu.Lock()
	defer mu.Unlock()
	if !found {
		t.Fatalf("no session_state_changed carried mover into %s; events=%v", kept.ID, seen)
	}
	placement, placed, err := w.d.store.SessionPlacement("mover")
	if err != nil || !placed || placement.DesktopID != kept.CurrentDesktopID {
		t.Fatalf("moved agent placement = %+v placed=%v err=%v, want destination desktop %s", placement, placed, err, kept.CurrentDesktopID)
	}

}

func TestInjectedSessionsLandOnTheCurrentDesktopOfTheirProfile(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	injectTestSession(t, d, protocol.Session{ID: "injected", Label: "injected", Directory: t.TempDir()})
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}
	if got := d.store.Get("injected").ProfileID; got != profile.ID {
		t.Fatalf("injected profile = %q, want %s", got, profile.ID)
	}
	if desktop := desktopOf(t, d, "injected"); desktop != profile.CurrentDesktopID {
		t.Fatalf("injected session on desktop %q, want the current desktop %s", desktop, profile.CurrentDesktopID)
	}
}

func TestImportedCrewJoinTheMostRecentlyUsedProfile(t *testing.T) {
	d := newCrewDaemon(t)
	profileID := defaultProfileID(t, d.store)
	for _, member := range crewList(t, d) {
		if member.ProfileID != profileID {
			t.Fatalf("imported member %s has profile %q, want %s", member.ID, member.ProfileID, profileID)
		}
	}
}

func TestWebSocketCommandsWithoutAProfileUseTheConnectionsProfile(t *testing.T) {
	w := newProfilesTestDaemon(t)
	w.d.ptyBackend = &fakeSpawnBackend{}
	client, _ := w.connect("")
	work := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "Work"}).Profile
	w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": work.ID})
	drainClientPayloads(t, client)

	data, err := json.Marshal(map[string]any{"cmd": "spawn_session", "id": "scoped", "cwd": t.TempDir(), "agent": "shell", "cols": 80, "rows": 24})
	if err != nil {
		t.Fatal(err)
	}
	w.d.handleClientMessage(client, data)
	expectSpawnResult(t, client, "scoped", true)
	if got := w.d.store.Get("scoped").ProfileID; got != work.ID {
		t.Fatalf("spawn without profile_id landed in %q, want the connection's profile %s", got, work.ID)
	}
}

func TestAppWakeFromAnotherProfileIsRefused(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	d.clientToken = "the-token"
	w := &profilesTestDaemon{t: t, d: d}
	client, _ := w.connect("")
	work := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "Work"}).Profile
	w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": work.ID})
	drainClientPayloads(t, client)

	wake := &protocol.CrewWakeMessage{Cmd: protocol.CmdCrewWake, Member: "trellis", RequestID: protocol.Ptr("wake-1")}
	wake.ProfileID = client.profileOr(wake.ProfileID)
	d.handleCrewWakeWS(client, wake)
	var result protocol.CrewWakeResultMessage
	for _, payload := range drainClientPayloads(t, client) {
		if eventName(t, payload) == protocol.EventCrewWakeResult {
			decodeInto(t, payload, &result)
		}
	}
	if result.Success || !strings.Contains(protocol.Deref(result.Error), work.ID) {
		t.Fatalf("wake from the Work profile = %+v, want a refusal naming it", result)
	}
	if spawnCount(backend) != 0 {
		t.Fatal("a refused wake spawned a session")
	}
}

func setTestChief(d *Daemon, sessionID string) error {
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		return err
	}
	if d.store.Get(sessionID) == nil {
		now := string(protocol.TimestampNow())
		d.store.Add(&protocol.Session{ID: sessionID, Label: sessionID, State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now})
	}
	if profileID, _ := d.store.SessionProfileID(sessionID); profileID == "" {
		if err := d.store.AssignSessionProfile(sessionID, profile.ID); err != nil {
			return err
		}
	}
	_, _, err = d.store.SetProfileChief(sessionID)
	return err
}

func TestSpawnBesideAFocusedTileDocksTheAgentBesideIt(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}
	desktop, err := d.store.GetDesktop(profile.CurrentDesktopID)
	if err != nil {
		t.Fatal(err)
	}
	notebook, err := d.resolvedDesktopTileDock(desktop.ID, desktopTileDock{tileID: "tile-notebook", tileKind: "notebook", edge: protocol.LayoutDockEdgeRight})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.UpdateDesktopArrangement(desktop.ID, desktop.Revision, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		return dockTileOnDesktop(desktop, notebook)
	}); err != nil {
		t.Fatal(err)
	}

	spawn := spawnCharacterizationMessage("beside-tile", profile.ID, cwd)
	spawn.Placement = &protocol.SessionPlacement{AnchorPaneID: protocol.Ptr("tile-notebook")}
	d.handleSpawnSession(client, spawn)

	result := expectSpawnResult(t, client, spawn.ID, true)
	if result.PlacementError != nil || result.PaneID == nil {
		t.Fatalf("spawn_result = %+v, want the agent placed beside the tile", result)
	}
	tree := desktopTree(t, d, desktop.ID)
	if tree.Type != "split" || tree.Children[0].TileID != "tile-notebook" || tree.Children[1].PaneID != protocol.Deref(result.PaneID) {
		t.Fatalf("desktop tree %+v, want the notebook then the new agent's pane", tree)
	}
}

package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/crew"
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
	now := string(protocol.TimestampNow())
	if err := json.NewEncoder(conn).Encode(protocol.InjectTestSessionMessage{
		Cmd: protocol.CmdInjectTestSession,
		Session: protocol.Session{
			ID: id, Label: label, Directory: dir, Agent: protocol.SessionAgentClaude,
			State: protocol.SessionStateLaunching, StateSince: now, StateUpdatedAt: now, LastSeen: now,
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

func recentProfileID(s *store.Store) string {
	profile, err := s.MostRecentlyUsedProfile()
	if err != nil {
		return ""
	}
	return profile.ID
}

func TestDelegationFromAnUnplacedSourceStartsUnplacedInTheSourcesProfile(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	setupDelegationGarden(t, d)
	consumeDelegatedPrompt(t, backend)
	work := createTestProfile(t, d.store, "Work")
	client := spawnTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: "work-source", Cwd: t.TempDir(), Agent: protocol.AgentShellValue,
		ProfileID: work.ID, Cols: 80, Rows: 24, Label: protocol.Ptr("Source"),
	})
	expectSpawnResult(t, client, "work-source", true)

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd: protocol.CmdDelegate, SourceSessionID: protocol.Ptr("work-source"),
		Brief: protocol.Ptr("Stay in my profile."), Agent: protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if protocol.Deref(result.ProfileID) != work.ID || d.store.Get(result.SessionID).ProfileID != work.ID {
		t.Fatalf("child profile = %q (row %q), want the source's %s", protocol.Deref(result.ProfileID), d.store.Get(result.SessionID).ProfileID, work.ID)
	}
	if desktop := desktopOf(t, d, result.SessionID); desktop != "" {
		t.Fatalf("child placed on %s, want it unplaced like its source", desktop)
	}
}

func TestDelegationWithoutASourceStartsUnplacedInTheMostRecentProfile(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	setupDelegationGarden(t, d)
	consumeDelegatedPrompt(t, backend)
	work := createTestProfile(t, d.store, "Work")
	if _, _, err := d.store.SelectProfile(work.ID); err != nil {
		t.Fatal(err)
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd: protocol.CmdDelegate, Brief: protocol.Ptr("Nobody sent me."), Agent: protocol.Ptr("codex"), Cwd: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if protocol.Deref(result.ProfileID) != work.ID {
		t.Fatalf("child profile = %q, want the most recently used %s", protocol.Deref(result.ProfileID), work.ID)
	}
	if desktop := desktopOf(t, d, result.SessionID); desktop != "" {
		t.Fatalf("child placed on %s, want it unplaced", desktop)
	}
}

func TestDeletingAProfileAnnouncesEveryMovedAgent(t *testing.T) {
	w := newProfilesTestDaemon(t)
	client, _ := w.connect("")
	doomed := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "doomed"}).Profile
	kept := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "kept"}).Profile
	w.agent("mover", doomed.ID)
	drainClientPayloads(t, client)
	trace := wireRecorder(w.d)

	w.mustSend(client, map[string]any{
		"cmd": protocol.CmdProfileDelete, "profile_id": doomed.ID, "expected_revision": doomed.Revision, "destination_profile_id": kept.ID,
	})
	found := false
	for i, name := range trace.EventNames() {
		if name != protocol.EventSessionStateChanged {
			continue
		}
		var event protocol.WebSocketEvent
		decodeInto(t, trace.Payloads()[i], &event)
		if event.Session != nil && event.Session.ID == "mover" && event.Session.ProfileID == kept.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no session_state_changed carried mover into %s; events=%v", kept.ID, trace.EventNames())
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

func TestCrewHomesJoinAProfileWhenImportedAndLegacyMembersAreGivenOne(t *testing.T) {
	d := newCrewDaemon(t)
	profileID := defaultProfileID(t, d.store)
	for _, member := range crewList(t, d) {
		if member.ProfileID != profileID {
			t.Fatalf("imported member %s has profile %q, want %s", member.ID, member.ProfileID, profileID)
		}
	}

	if _, err := d.updateCrewMember("trellis", func(m *crew.Member) (bool, error) {
		m.ProfileID = ""
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	d.assignCrewProfiles()
	if got := memberByID(t, crewList(t, d), "trellis").ProfileID; got != profileID {
		t.Fatalf("legacy member profile = %q, want %s", got, profileID)
	}
}

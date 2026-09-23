package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
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

func TestSpawnResultReportsWhereTheAgentLandedOrWhyItDidNot(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}
	first := spawnCharacterizationMessage("landed", profile.ID, cwd)
	first.Placement = &protocol.SessionPlacement{}
	d.handleSpawnSession(client, first)
	landed := expectSpawnResult(t, client, first.ID, true)
	placement, _, _ := d.store.SessionPlacement(first.ID)
	if protocol.Deref(landed.DesktopID) != profile.CurrentDesktopID || protocol.Deref(landed.PaneID) != placement.PaneID || landed.PlacementError != nil {
		t.Fatalf("spawn_result = %+v, want the desktop and pane it landed in (%+v)", landed, placement)
	}

	second := spawnCharacterizationMessage("anchor-vanished", profile.ID, cwd)
	second.Placement = &protocol.SessionPlacement{AnchorPaneID: protocol.Ptr(placement.PaneID)}
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == second.ID {
			if _, err := d.store.RemoveSessionPlacement(first.ID); err != nil {
				t.Errorf("remove the anchor: %v", err)
			}
		}
	}
	d.handleSpawnSession(client, second)
	unplaced := expectSpawnResult(t, client, second.ID, true)
	if unplaced.PaneID != nil || !strings.Contains(protocol.Deref(unplaced.PlacementError), placement.PaneID) {
		t.Fatalf("spawn_result = %+v, want no pane and an error naming the missing anchor %s", unplaced, placement.PaneID)
	}
	if session := d.store.Get(second.ID); session == nil || session.ProfileID != profile.ID {
		t.Fatalf("the agent whose placement failed = %+v, want it running unplaced in its profile", session)
	}

	_, doomed, err := d.store.CreateDesktop(profile.ID, "doomed", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	third := spawnCharacterizationMessage("desktop-vanished", profile.ID, cwd)
	third.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(doomed.ID)}
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == third.ID {
			if _, err := d.store.DeleteDesktop(doomed.ID, doomed.Revision); err != nil {
				t.Errorf("delete the target desktop: %v", err)
			}
		}
	}
	d.handleSpawnSession(client, third)
	orphaned := expectSpawnResult(t, client, third.ID, true)
	if orphaned.PaneID != nil || !strings.Contains(protocol.Deref(orphaned.PlacementError), doomed.ID) {
		t.Fatalf("spawn_result = %+v, want no pane and an error naming the deleted desktop %s", orphaned, doomed.ID)
	}
	if _, placed, _ := d.store.SessionPlacement(third.ID); placed {
		t.Fatal("the agent whose desktop was deleted was placed anyway")
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

func TestDeletingAProfileCarriesItsAutomationsAndCrewToTheDestination(t *testing.T) {
	d := newCrewDaemon(t)
	d.ptyBackend = &fakeSpawnBackend{}
	w := &profilesTestDaemon{t: t, d: d}
	d.clientToken = "the-token"
	client, _ := w.connect("")
	doomed := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "doomed"}).Profile
	defaultID := defaultProfileID(t, d.store)
	now := time.Now()
	definition, err := d.store.UpsertAutomationDefinition("nightly", "Nightly", `{}`, doomed.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	pending, _, err := d.store.ClaimManualAutomationRun(definition.ID, "req-1", "", `{}`, definition.Revision, `{}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "seed-1", SessionID: "session-1", ProfileID: doomed.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	d.setCrewProfile("trellis", defaultID, doomed.ID)

	w.mustSend(client, map[string]any{
		"cmd": protocol.CmdProfileDelete, "profile_id": doomed.ID, "expected_revision": doomed.Revision, "destination_profile_id": defaultID,
	})
	if moved, err := d.store.GetAutomationDefinition(definition.ID); err != nil || moved.ProfileID != defaultID {
		t.Fatalf("definition after delete = %+v err=%v, want it in %s", moved, err, defaultID)
	}
	if run, err := d.store.GetAutomationRun(pending.ID); err != nil || run.ProfileID != defaultID {
		t.Fatalf("pending run after delete = %+v err=%v, want it in %s", run, err, defaultID)
	}
	if got := memberByID(t, crewList(t, d), "trellis").ProfileID; got != defaultID {
		t.Fatalf("crew member profile after delete = %q, want %s", got, defaultID)
	}
	if _, err := d.newAutomationRunReservation(definition); err == nil {
		t.Fatal("the stale definition value still reserved into the deleted profile")
	}
	current, _ := d.store.GetAutomationDefinition(definition.ID)
	if _, err := d.newAutomationRunReservation(current); err != nil {
		t.Fatalf("reserving a run after the move: %v", err)
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

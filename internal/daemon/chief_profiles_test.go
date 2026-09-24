package daemon

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func spawnChiefCandidate(t *testing.T, d *Daemon, client *wsClient, sessionID, profileID string) {
	t.Helper()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: sessionID, Label: protocol.Ptr(sessionID), Cwd: t.TempDir(),
		Agent: string(protocol.SessionAgentClaude), ProfileID: profileID, Cols: 80, Rows: 24,
		ChiefOfStaff: protocol.Ptr(true),
	})
	expectSpawnResult(t, client, sessionID, true)
}

func TestEachProfileKeepsItsOwnChief(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	home := defaultProfileID(t, d.store)
	work := createTestProfile(t, d.store, "Work")

	spawnChiefCandidate(t, d, client, "home-chief", home)
	spawnChiefCandidate(t, d, client, "work-chief", work.ID)
	spawnChiefCandidate(t, d, client, "work-second", work.ID)

	if d.chiefOfProfile(home) != "home-chief" || d.chiefOfProfile(work.ID) != "work-chief" {
		t.Fatalf("chiefs = home %q, work %q; want home-chief and work-chief", d.chiefOfProfile(home), d.chiefOfProfile(work.ID))
	}
	if d.isChiefOfStaffSession("work-second") {
		t.Fatal("a second create-as-chief in Work took the role from its chief")
	}
	for _, id := range []string{"home-chief", "work-chief"} {
		if decorated := d.sessionForBroadcast(d.store.Get(id)); decorated.ChiefOfStaff == nil || !*decorated.ChiefOfStaff {
			t.Fatalf("%s broadcasts chief_of_staff = %v, want true", id, decorated.ChiefOfStaff)
		}
	}

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{Cmd: protocol.CmdSetChiefOfStaff, SessionID: "work-second", ChiefOfStaff: true})
	var result protocol.ChiefOfStaffResultMessage
	for _, payload := range drainClientPayloads(t, client) {
		if eventName(t, payload) == protocol.EventChiefOfStaffResult {
			decodeInto(t, payload, &result)
		}
	}
	if !result.Success || protocol.Deref(result.PreviousSessionID) != "work-chief" {
		t.Fatalf("set chief result = %+v, want success replacing work-chief", result)
	}
	if d.chiefOfProfile(work.ID) != "work-second" || d.chiefOfProfile(home) != "home-chief" {
		t.Fatalf("after the Work change: home %q, work %q; want home-chief kept and work-second", d.chiefOfProfile(home), d.chiefOfProfile(work.ID))
	}
}

func TestChiefLookupsFollowTheirCaller(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	home := defaultProfileID(t, d.store)
	work := createTestProfile(t, d.store, "Work")
	spawnChiefCandidate(t, d, client, "home-chief", home)
	spawnChiefCandidate(t, d, client, "work-chief", work.ID)
	d.handleSpawnSession(client, spawnCharacterizationMessage("work-agent", work.ID, t.TempDir()))
	expectSpawnResult(t, client, "work-agent", true)

	if got := d.chiefForCaller("work-agent"); got != "work-chief" {
		t.Fatalf("an agent in Work reaches chief %q, want work-chief", got)
	}
	client.selectProfile(work.ID)
	if got := d.chiefForClient(client); got != "work-chief" {
		t.Fatalf("an app scoped to Work reaches chief %q, want work-chief", got)
	}
	if _, err := d.store.SelectProfile(home); err != nil {
		t.Fatal(err)
	}
	if got := d.chiefForCaller(""); got != "home-chief" {
		t.Fatalf("with no caller the most recently used profile's chief is %q, want home-chief", got)
	}
}

func TestAChiefsTicketIdentityCarriesItsProfile(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	home := defaultProfileID(t, d.store)
	work := createTestProfile(t, d.store, "Work")
	spawnChiefCandidate(t, d, client, "home-chief", home)
	spawnChiefCandidate(t, d, client, "work-chief", work.ID)
	legacy := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)

	if got := d.ticketDurableIdentitiesForSession("work-chief"); !slices.Equal(got, []string{store.TicketChiefIdentity(work.ID)}) {
		t.Fatalf("Work's chief observes tickets as %v, want only Work's chief identity", got)
	}
	if got := d.ticketDurableIdentitiesForSession("home-chief"); !slices.Equal(got, []string{legacy, store.TicketChiefIdentity(home)}) {
		t.Fatalf("Default's chief observes tickets as %v, want the legacy identity and its profile's", got)
	}
	for identity, want := range map[string]string{
		store.TicketChiefIdentity(work.ID): "work-chief",
		store.TicketChiefIdentity(home):    "home-chief",
		legacy:                             "home-chief",
	} {
		if got := d.ticketSessionForIdentity(identity); got != want {
			t.Fatalf("ticket identity %s reaches %q, want %q", identity, got, want)
		}
	}
}

func TestDeletingAProfileDemotesItsChiefAndKeepsTheDestinations(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	home := defaultProfileID(t, d.store)
	work := createTestProfile(t, d.store, "Work")
	spawnChiefCandidate(t, d, client, "home-chief", home)
	spawnChiefCandidate(t, d, client, "work-chief", work.ID)
	current, err := d.store.GetProfile(work.ID)
	if err != nil {
		t.Fatal(err)
	}

	d.handleProfileDelete(client, &protocol.ProfileDeleteMessage{
		Cmd: protocol.CmdProfileDelete, RequestID: "delete-work", ProfileID: work.ID, ExpectedRevision: int(current.Revision), DestinationProfileID: home,
	})

	if profileID, _ := d.store.SessionProfileID("work-chief"); profileID != home {
		t.Fatalf("Work's chief moved to %q, want %s", profileID, home)
	}
	if d.chiefOfProfile(home) != "home-chief" {
		t.Fatalf("the destination's chief is %q, want home-chief kept", d.chiefOfProfile(home))
	}
	if d.isChiefOfStaffSession("work-chief") {
		t.Fatal("the deleted profile's chief kept the role in the destination")
	}
	if decorated := d.sessionForBroadcast(d.store.Get("work-chief")); decorated.ChiefOfStaff != nil {
		t.Fatalf("the deleted profile's chief broadcasts chief_of_staff = %v", *decorated.ChiefOfStaff)
	}
}

func TestAChiefClosesOnlyAgentsOfItsProfile(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	home := defaultProfileID(t, d.store)
	work := createTestProfile(t, d.store, "Work")
	spawnChiefCandidate(t, d, client, "home-chief", home)
	for id, profileID := range map[string]string{"home-agent": home, "work-agent": work.ID} {
		d.handleSpawnSession(client, spawnCharacterizationMessage(id, profileID, t.TempDir()))
		expectSpawnResult(t, client, id, true)
	}

	chief := d.store.Get("home-chief")
	if rule, err := d.agentCloseRule(chief, d.store.Get("home-agent")); err != nil || rule != protocol.AgentCloseRuleChiefOfStaff {
		t.Fatalf("home chief closing a home agent = %q, %v; want the chief rule", rule, err)
	}
	if rule, err := d.agentCloseRule(chief, d.store.Get("work-agent")); err == nil {
		t.Fatalf("home chief closing a Work agent was allowed by %q", rule)
	}
}

func TestMovingAProfilesChiefOutDemotesItAndKeepsTheDestinationsChief(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	home := defaultProfileID(t, d.store)
	work := createTestProfile(t, d.store, "Work")
	spawnChiefCandidate(t, d, client, "home-chief", home)
	spawnChiefCandidate(t, d, client, "work-chief", work.ID)
	drainClientPayloads(t, client)

	d.handleSessionMove(client, &protocol.SessionMoveMessage{
		Cmd: protocol.CmdSessionMove, RequestID: "move-work-chief", SessionID: "work-chief", ExpectedProfileID: work.ID, DestinationProfileID: home,
	})

	if profileID, _ := d.store.SessionProfileID("work-chief"); profileID != home {
		t.Fatalf("Work's chief moved to %q, want %s", profileID, home)
	}
	if d.chiefOfProfile(work.ID) != "" {
		t.Fatalf("Work still names %q as its chief after it moved out", d.chiefOfProfile(work.ID))
	}
	if d.chiefOfProfile(home) != "home-chief" {
		t.Fatalf("the destination's chief is %q, want home-chief kept", d.chiefOfProfile(home))
	}
	if d.isChiefOfStaffSession("work-chief") {
		t.Fatal("the moved chief kept the role in the destination")
	}
	if got := d.ticketSessionForIdentity(store.TicketChiefIdentity(work.ID)); got != "" {
		t.Fatalf("Work's chief tickets still reach %q", got)
	}
}

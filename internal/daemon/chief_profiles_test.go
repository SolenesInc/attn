package daemon

import (
	"path/filepath"
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
	if got := d.ticketSessionForIdentity(store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)); got != "home-chief" {
		t.Fatalf("a legacy chief ticket reaches %q, want the Default profile's chief home-chief", got)
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

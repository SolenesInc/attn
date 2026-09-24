package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func pinnedAt(t *testing.T, d *Daemon, id string) string {
	t.Helper()
	session := d.sessionForBroadcast(d.store.Get(id))
	if session == nil {
		t.Fatalf("session %s not found", id)
	}
	return protocol.Deref(session.PinnedAt)
}

func TestPinningOneSessionLeavesItsSiblingsInTheQueue(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "pinned", protocol.SessionAgentCodex, "ws1")
	addTurnSession(t, d, "sibling", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "pinned", protocol.StateWaitingInput)
	moveTo(d, "sibling", protocol.StateWaitingInput)

	if errMsg := d.setSessionPinned("pinned", true); errMsg != "" {
		t.Fatalf("pin failed: %s", errMsg)
	}

	if owed(t, d, "pinned") {
		t.Error("a pinned session still owes a turn")
	}
	if !owed(t, d, "sibling") {
		t.Error("pinning one session took its sibling out of the queue too")
	}
	if pinnedAt(t, d, "pinned") == "" {
		t.Error("pinned_at is absent on the wire, so the sidebar cannot place the row")
	}
	if pinnedAt(t, d, "sibling") != "" {
		t.Error("pinned_at is set on a session nobody pinned")
	}
}

func TestUnpinningSurfacesTheOutstandingTurnAtItsTrueAge(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	openedAt := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt)
	if openedAt == "" {
		t.Fatal("no turn to pin over")
	}

	if errMsg := d.setSessionPinned("s1", true); errMsg != "" {
		t.Fatalf("pin failed: %s", errMsg)
	}
	if owed(t, d, "s1") {
		t.Fatal("a pinned session still owes a turn")
	}

	if errMsg := d.setSessionPinned("s1", false); errMsg != "" {
		t.Fatalf("unpin failed: %s", errMsg)
	}
	if !owed(t, d, "s1") {
		t.Fatal("unpinning lost the outstanding turn")
	}
	if got := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt); got != openedAt {
		t.Errorf("turn_opened_at = %q after unpin, want the original %q", got, openedAt)
	}
}

func TestAPinnedSessionStillAccumulatesTurns(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	if errMsg := d.setSessionPinned("s1", true); errMsg != "" {
		t.Fatalf("pin failed: %s", errMsg)
	}
	moveTo(d, "s1", protocol.StateWaitingInput)
	if owed(t, d, "s1") {
		t.Fatal("a pinned session owes a turn on the wire")
	}
	if d.store.TurnStamps("s1").OpenedAt.IsZero() {
		t.Fatal("no turn was stamped while pinned; unpinning would surface nothing")
	}
}

func TestPinningTheChiefIsRefused(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "chief", protocol.SessionAgentCodex, "ws1")
	if err := setTestChief(d, "chief"); err != nil {
		t.Fatalf("assign the chief role: %v", err)
	}

	if errMsg := d.setSessionPinned("chief", true); errMsg == "" {
		t.Fatal("pinning the chief was accepted; it is already anchored above the queue")
	}
	if pinnedAt(t, d, "chief") != "" {
		t.Error("the refused pin was written anyway")
	}
}

func TestPinningReportsAMissingSession(t *testing.T) {
	d := newTurnDaemon(t)
	if errMsg := d.setSessionPinned("nobody", true); errMsg == "" {
		t.Fatal("pinning a session that does not exist was accepted")
	}
}

func TestResolveSpawnParent(t *testing.T) {
	d := newTurnDaemon(t)
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := d.store.CreateDesktop(profile.ID, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	addTurnSession(t, d, "agent", protocol.SessionAgentCodex, "")
	addTurnSession(t, d, "shell1", protocol.SessionAgentShell, "")
	shell := d.store.Get("shell1")
	shell.ParentSessionID = protocol.Ptr("agent")
	d.store.Add(shell)
	addTurnSession(t, d, "orphan-shell", protocol.SessionAgentShell, "")
	addTurnSession(t, d, "unplaced", protocol.SessionAgentCodex, "")
	placeTestSession(t, d, "agent", profile.CurrentDesktopID)

	here := &launchPlacement{desktopID: profile.CurrentDesktopID}
	current := &launchPlacement{}
	there := &launchPlacement{desktopID: other.ID}
	tests := []struct {
		name        string
		spawnedFrom string
		placement   *launchPlacement
		isShell     bool
		want        string
	}{
		{"split from an agent", "agent", here, true, "agent"},
		{"landing on the profile's current desktop", "agent", current, true, "agent"},
		{"split from a shell inherits that shell's agent", "shell1", here, true, "agent"},
		{"split from a shell with no agent of its own", "orphan-shell", here, true, ""},
		{"an agent is never a satellite", "agent", here, false, ""},
		{"no base at all", "", here, true, ""},
		{"a base that is gone", "vanished", here, true, ""},
		{"landing on another desktop", "agent", there, true, ""},
		{"an unplaced shell", "agent", nil, true, ""},
		{"a parent with no placement", "unplaced", here, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.resolveSpawnParent(tt.spawnedFrom, profile, tt.placement, tt.isShell); got != tt.want {
				t.Errorf("resolveSpawnParent(%q, %+v, %v) = %q, want %q",
					tt.spawnedFrom, tt.placement, tt.isShell, got, tt.want)
			}
		})
	}
}

func TestASatelliteNeverOwesATurn(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "agent", protocol.SessionAgentCodex, "ws1")
	addTurnSession(t, d, "shell1", protocol.SessionAgentShell, "ws1")
	shell := d.store.Get("shell1")
	shell.ParentSessionID = protocol.Ptr("agent")
	d.store.Add(shell)

	moveTo(d, "shell1", protocol.StateIdle)
	if owed(t, d, "shell1") {
		t.Fatal("a shell owes a turn")
	}
	if got := protocol.Deref(d.sessionForBroadcast(d.store.Get("shell1")).ParentSessionID); got != "agent" {
		t.Errorf("parent_session_id = %q on the wire, want %q — the app cannot place the row without it", got, "agent")
	}
}

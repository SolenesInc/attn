package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

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

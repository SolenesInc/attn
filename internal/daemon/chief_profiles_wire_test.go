package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestEachProfileKeepsItsOwnChief(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	homeApp := w.App()
	home := homeApp.SelectedProfile()
	work := createProfile(homeApp, "Work")
	workApp := w.AppOn(work.ID)
	asChief := func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) }
	homeChief := w.Spawn(homeApp, fakeagent.Claude, w.Path("home-chief"), asChief)
	w.Launched(homeChief)
	workChief := w.Spawn(workApp, fakeagent.Claude, w.Path("work-chief"), asChief)
	w.Launched(workChief)
	workSecond := w.Spawn(workApp, fakeagent.Claude, w.Path("work-second"), asChief)
	w.Launched(workSecond)
	chiefOf := func(profile, want string) {
		t.Helper()
		var chiefs []string
		for _, s := range w.AppOn(profile).Initial.Sessions {
			if s.ProfileID == profile && protocol.Deref(s.ChiefOfStaff) {
				chiefs = append(chiefs, string(s.ID))
			}
		}
		if !slices.Equal(chiefs, []string{want}) {
			t.Fatalf("profile %s chiefs=%v, want %s", profile, chiefs, want)
		}
	}
	chiefOf(home, homeChief)
	chiefOf(work.ID, workChief)
	changed := setChiefOfStaff(workApp, workSecond, true)
	if !changed.Success || string(protocol.Deref(changed.PreviousSessionID)) != workChief {
		t.Fatalf("change the Work chief: %+v", changed)
	}
	w.Launched(workChief)
	w.Launched(workSecond)
	chiefOf(home, homeChief)
	chiefOf(work.ID, workSecond)
}

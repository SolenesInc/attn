package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const ventGuidanceLead = "say so with `attn vent`"

func TestAnAgentIsTaughtToVentOnlyWhenItLaunchedWithAnalyticsOn(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	taught := func(run *fakeagent.Run) bool { return strings.Contains(launchGuidance(t, run), ventGuidanceLead) }
	respawn := func(session string) *fakeagent.Run {
		t.Helper()
		app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(session)})
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
		reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
			protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
		if !reloaded.Success {
			t.Fatalf("respawning %s: %s", session, protocol.Deref(reloaded.Error))
		}
		return w.Launched(session)
	}
	without := w.Spawn(app, fakeagent.Claude, w.Path("before"))
	if taught(w.Launched(without)) {
		t.Fatal("an agent launched with analytics off was taught to vent")
	}

	setSetting(t, app, "analytics.enabled", "true")
	with := w.Spawn(app, fakeagent.Claude, w.Path("after"))
	if !taught(w.Launched(with)) {
		t.Fatal("an agent launched with analytics on was not taught to vent")
	}
	if taught(respawn(without)) {
		t.Fatal("an agent launched with analytics off was taught to vent when it came back")
	}
	if !taught(respawn(with)) {
		t.Fatal("an agent launched with analytics on lost its vent guidance when it came back")
	}

	setSetting(t, app, "analytics.enabled", "false")
	if _, err := cli.RecordVent(protocol.SessionID(with), "the build is slow"); err == nil || !strings.Contains(err.Error(), "usage analytics is off") {
		t.Fatalf("an agent vented with analytics off: %v", err)
	}
}

func TestTheUserDismissesAndRestoresAVentInTheirProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "analytics.enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(session)
	vent, err := cli.RecordVent(protocol.SessionID(session), "attn seed tend refused a seed held by a session that ended.")
	if err != nil {
		t.Fatalf("vent: %v", err)
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool { return len(m.Vents) == 1 })

	dismiss := func(peer *testworld.Peer, request string, dismissed bool) protocol.SetVentDismissedResultMessage {
		t.Helper()
		return testworld.Request(peer, protocol.SetVentDismissedMessage{Cmd: protocol.CmdSetVentDismissed, RequestID: request, VentID: vent.ID, Dismissed: dismissed},
			protocol.EventSetVentDismissedResult, func(r protocol.SetVentDismissedResultMessage) bool { return r.RequestID == request })
	}
	if result := dismiss(app, "dismiss", true); !result.Success {
		t.Fatalf("dismissing failed: %s", protocol.Deref(result.Error))
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool { return len(m.Vents) == 1 && m.Vents[0].DismissedAt != nil })
	if result := dismiss(app, "restore", false); !result.Success {
		t.Fatalf("restoring failed: %s", protocol.Deref(result.Error))
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool { return len(m.Vents) == 1 && m.Vents[0].DismissedAt == nil })

	other := w.AppOn(createProfile(app, "Work").ID)
	if result := dismiss(other, "elsewhere", true); result.Success || !strings.Contains(protocol.Deref(result.Error), "no vent "+vent.ID+" in this profile") {
		t.Errorf("another profile dismissed the vent: %+v", result)
	}
}

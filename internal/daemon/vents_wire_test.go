package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const ventGuidanceLead = "with `attn vent --about attn"

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
	if _, err := cli.RecordVent(protocol.SessionID(with), protocol.VentKindAttn, "the build is slow"); err == nil || !strings.Contains(err.Error(), "usage analytics is off") {
		t.Fatalf("an agent vented with analytics off: %v", err)
	}
}

func TestTheUserDismissesAndRestoresAVentInTheirProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "analytics.enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(session)
	vent, err := cli.RecordVent(protocol.SessionID(session), protocol.VentKindAttn, "attn seed tend refused a seed held by a session that ended.")
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

func TestTheUserPlantsOneSeedFromAVent(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "analytics.enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(session)
	vent, err := cli.RecordVent(protocol.SessionID(session), protocol.VentKindAttn, "attn seed tend refused a seed held by a session that ended.\nRetried twice, then used --force.")
	if err != nil {
		t.Fatalf("vent: %v", err)
	}
	plant := func(peer *testworld.Peer, request, id string) protocol.PlantVentSeedResultMessage {
		t.Helper()
		return testworld.Request(peer, protocol.PlantVentSeedMessage{Cmd: protocol.CmdPlantVentSeed, RequestID: request, VentID: id},
			protocol.EventPlantVentSeedResult, func(r protocol.PlantVentSeedResultMessage) bool { return r.RequestID == request })
	}

	planted := plant(app, "plant", vent.ID)
	if !planted.Success || planted.Seed == nil {
		t.Fatalf("planting from the vent: %s", protocol.Deref(planted.Error))
	}
	shown, err := cli.SeedShow("", planted.Seed.ID)
	if err != nil {
		t.Fatalf("the planted seed: %v", err)
	}
	if shown.Seed.Title != "attn seed tend refused a seed held by a session that ended." ||
		!strings.HasPrefix(shown.Seed.Body, vent.Text+"\n\n## Context\n") || !strings.Contains(shown.Seed.Body, "- Vent: "+vent.ID) {
		t.Errorf("the planted seed reads %q / %q, want the vent's first line and its text with context", shown.Seed.Title, shown.Seed.Body)
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool {
		return len(m.Vents) == 1 && protocol.Deref(m.Vents[0].PlantedSeedID) == planted.Seed.ID
	})

	if again := plant(app, "again", vent.ID); again.Success || protocol.Deref(again.Error) != "vent "+vent.ID+" already planted seed "+planted.Seed.ID {
		t.Errorf("planting the vent twice: %+v", again)
	}
	other := w.AppOn(createProfile(app, "Work").ID)
	if elsewhere := plant(other, "elsewhere", vent.ID); elsewhere.Success || protocol.Deref(elsewhere.Error) != "no vent "+vent.ID+" in this profile" {
		t.Errorf("another profile planted from the vent: %+v", elsewhere)
	}

	unplanted := testworld.Request(app, protocol.UnplantVentSeedMessage{Cmd: protocol.CmdUnplantVentSeed, RequestID: "undo", VentID: vent.ID},
		protocol.EventUnplantVentSeedResult, func(r protocol.UnplantVentSeedResultMessage) bool { return r.RequestID == "undo" })
	if !unplanted.Success {
		t.Fatalf("undoing the plant: %s", protocol.Deref(unplanted.Error))
	}
	if withered, err := cli.WithRequester(app.SelectedProfile(), "").SeedShow("", planted.Seed.ID); err != nil || withered.Seed.Status != string(garden.StatusWithered) {
		t.Errorf("after undo the seed is %+v, %v; want withered", withered, err)
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool {
		return len(m.Vents) == 1 && m.Vents[0].PlantedSeedID == nil
	})
	if replanted := plant(app, "replant", vent.ID); !replanted.Success || replanted.Seed.ID == planted.Seed.ID {
		t.Errorf("planting again after undo: %+v", replanted)
	}
}

func TestClearingTheLocalRecordDeletesThisProfilesDismissedVents(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "analytics.enabled", "true")
	vent := func(peer *testworld.Peer, dir, text string) protocol.Vent {
		t.Helper()
		session := w.Spawn(peer, fakeagent.Claude, w.Path(dir))
		w.Launched(session)
		recorded, err := cli.RecordVent(protocol.SessionID(session), protocol.VentKindAttn, text)
		if err != nil {
			t.Fatalf("vent: %v", err)
		}
		return *recorded
	}
	dismiss := func(peer *testworld.Peer, id string) {
		t.Helper()
		result := testworld.Request(peer, protocol.SetVentDismissedMessage{Cmd: protocol.CmdSetVentDismissed, RequestID: "dismiss-" + id, VentID: id, Dismissed: true},
			protocol.EventSetVentDismissedResult, func(r protocol.SetVentDismissedResultMessage) bool { return r.RequestID == "dismiss-"+id })
		if !result.Success {
			t.Fatalf("dismissing %s: %s", id, protocol.Deref(result.Error))
		}
	}
	kept := vent(app, "kept", "the build is slow")
	dismissed := vent(app, "dismissed", "a flaky test")
	dismiss(app, dismissed.ID)
	workID := createProfile(app, "Work").ID
	work := w.AppOn(workID)
	elsewhere := vent(work, "elsewhere", "another profile's vent")
	dismiss(work, elsewhere.ID)

	cleared := testworld.Request(app, protocol.ClearAnalyticsRecordsMessage{Cmd: protocol.CmdClearAnalyticsRecords, RequestID: "clear"},
		protocol.EventClearAnalyticsRecordsResult, func(r protocol.ClearAnalyticsRecordsResultMessage) bool { return r.RequestID == "clear" })
	if !cleared.Success || cleared.Vents != 1 {
		t.Fatalf("clear = %+v, want one dismissed vent deleted", cleared)
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool {
		return len(m.Vents) == 1 && m.Vents[0].ID == kept.ID
	})
	if left := w.AppOn(workID).Initial.Vents; len(left) != 1 || left[0].ID != elsewhere.ID {
		t.Errorf("clearing in Default touched Work's vents: %+v", left)
	}
}

func TestOpenAppsDropADismissedVentThirtyDaysOn(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		setAnalyticsEnabled(app, "true")
		agent := w.bubbleClaude(t, app, "shop")
		vent, err := agent.cli.RecordVent(protocol.SessionID(agent.id), protocol.VentKindAttn, "a flaky test")
		if err != nil {
			t.Fatalf("vent: %v", err)
		}
		dismissed := testworld.Request(app, protocol.SetVentDismissedMessage{Cmd: protocol.CmdSetVentDismissed, RequestID: "dismiss", VentID: vent.ID, Dismissed: true},
			protocol.EventSetVentDismissedResult, func(r protocol.SetVentDismissedResultMessage) bool { return r.RequestID == "dismiss" })
		if !dismissed.Success {
			t.Fatalf("dismissing: %s", protocol.Deref(dismissed.Error))
		}
		testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool { return len(m.Vents) == 1 && m.Vents[0].DismissedAt != nil })

		w.stop()
		w.elapse(30 * 24 * time.Hour)
		w.start()
		reopened := w.App()
		if len(reopened.Initial.Vents) != 1 {
			t.Fatalf("before the prune runs the app shows %d vents, want the dismissed one", len(reopened.Initial.Vents))
		}
		w.advance(time.Hour + time.Minute)
		testworld.Await(reopened, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool { return len(m.Vents) == 0 })
	})
}

func TestToolVentsNeedTheUsersSettingAndNeverLeaveTheMachine(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "analytics.enabled", "true")
	launch := func(dir string) (string, string) {
		t.Helper()
		session := w.Spawn(app, fakeagent.Claude, w.Path(dir))
		return session, launchGuidance(t, w.Launched(session))
	}
	attnOnly, taught := launch("attn-only")
	if strings.Contains(taught, "--about tools") || !strings.Contains(taught, "are not vents") {
		t.Fatalf("with tool vents off the agent was taught:\n%s", taught)
	}
	if _, err := cli.RecordVent(protocol.SessionID(attnOnly), protocol.VentKindTools, "the ship-pr skill links a missing skill"); err == nil ||
		!strings.Contains(err.Error(), "tool vents are off for this session (analytics.tool_vents)") {
		t.Fatalf("a tool vent with tool vents off: %v", err)
	}

	setSetting(t, app, "analytics.tool_vents", "true")
	if _, err := cli.RecordVent(protocol.SessionID(attnOnly), protocol.VentKindTools, "the ship-pr skill links a missing skill"); err == nil {
		t.Fatal("a session launched with tool vents off sent a tool vent after the user turned them on")
	}
	setSetting(t, app, "analytics.sharing_enabled", "true")
	setSetting(t, app, "analytics.share_vents", "true")
	withTools, taught := launch("with-tools")
	if !strings.Contains(taught, "attn vent --about tools") {
		t.Fatalf("with tool vents on the agent was not taught them:\n%s", taught)
	}
	tool, err := cli.RecordVent(protocol.SessionID(withTools), protocol.VentKindTools, "the ship-pr skill links a missing skill")
	if err != nil {
		t.Fatalf("tool vent: %v", err)
	}
	if got := streamOutbox(t, app, analytics.StreamVent); len(got) != 0 {
		t.Fatalf("with sharing and Send vents on a tool vent queued %v", got)
	}
	if tool.Kind != protocol.VentKindTools {
		t.Fatalf("the tool vent is filed as %q", tool.Kind)
	}
	if sent := setVentSend(app, tool.ID, true); sent.Success || !strings.Contains(protocol.Deref(sent.Error), "about the agent's other tools, so it stays on this machine") {
		t.Fatalf("sending a tool vent = success %v, error %q; want it kept here", sent.Success, protocol.Deref(sent.Error))
	}

	attn, err := cli.RecordVent(protocol.SessionID(attnOnly), protocol.VentKindAttn, "attn seed check report refused a 516-character summary.")
	if err != nil {
		t.Fatalf("attn vent: %v", err)
	}
	if sent := setVentSend(app, attn.ID, true); !sent.Success {
		t.Fatalf("sending the attn vent: %s", protocol.Deref(sent.Error))
	}
	if refiled := setVentKind(app, attn.ID, protocol.VentKindTools); !refiled.Success {
		t.Fatalf("filing the queued attn vent under tools: %s", protocol.Deref(refiled.Error))
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool {
		return len(m.Vents) == 2 && m.Vents[0].ID == attn.ID && m.Vents[0].Kind == protocol.VentKindTools && m.Vents[0].Send == nil
	})
	if got := streamOutbox(t, app, analytics.StreamVent); len(got) != 0 {
		t.Fatalf("after filing the vent under tools the outbox holds vent records %v", got)
	}
	if refiled := setVentKind(app, attn.ID, protocol.VentKindAttn); !refiled.Success {
		t.Fatalf("filing the vent back under attn: %s", protocol.Deref(refiled.Error))
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool {
		return len(m.Vents) == 2 && m.Vents[0].Kind == protocol.VentKindAttn && m.Vents[0].Send == nil
	})
}

func setVentKind(app *testworld.Peer, id string, kind protocol.VentKind) protocol.SetVentKindResultMessage {
	app.T.Helper()
	request := "kind-" + id + "-" + string(kind)
	return testworld.Request(app, protocol.SetVentKindMessage{Cmd: protocol.CmdSetVentKind, RequestID: request, VentID: id, Kind: kind},
		protocol.EventSetVentKindResult, func(r protocol.SetVentKindResultMessage) bool { return r.RequestID == request })
}

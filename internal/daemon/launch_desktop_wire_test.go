package daemon_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/profilemigration"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func launchSetting(app *testworld.Peer, kind, id, mode, desktop string) protocol.LaunchDesktopItem {
	app.T.Helper()
	req := protocol.LaunchDesktopSetMessage{Cmd: protocol.CmdLaunchDesktopSet, RequestID: "set-" + kind + id, Kind: protocol.LaunchDesktopKind(kind), ItemID: id,
		Setting: protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopMode(mode)}}
	if desktop != "" {
		req.Setting.DesktopID = protocol.Ptr(desktop)
	}
	result := testworld.Request(app, req, protocol.EventLaunchDesktopResult, func(r protocol.LaunchDesktopResultMessage) bool { return r.RequestID == req.RequestID })
	if !result.Success || result.Item == nil {
		app.T.Fatalf("launch setting: %+v", result)
	}
	return *result.Item
}

func readLaunchSetting(app *testworld.Peer, kind, id string) protocol.LaunchDesktopItem {
	app.T.Helper()
	req := protocol.LaunchDesktopGetMessage{Cmd: protocol.CmdLaunchDesktopGet, RequestID: "get-" + kind + id, Kind: protocol.LaunchDesktopKind(kind), ItemID: id}
	result := testworld.Request(app, req, protocol.EventLaunchDesktopResult, func(r protocol.LaunchDesktopResultMessage) bool { return r.RequestID == req.RequestID })
	if !result.Success || result.Item == nil {
		app.T.Fatalf("read launch setting: %+v", result)
	}
	return *result.Item
}

func assertBackgroundPlacement(t *testing.T, w *world, profileID, sessionID, target, current, active string) {
	t.Helper()
	view := viewProfile(t, w, profileID)
	desktop, _ := view.paneOf(t, sessionID)
	if desktop.ID != target || view.profile.CurrentDesktopID != current || view.desktops[current].ActivePaneID != active {
		t.Fatalf("launch %s placed on %s, current=%s active=%s; want target=%s current=%s active=%s", sessionID, desktop.ID, view.profile.CurrentDesktopID, view.desktops[current].ActivePaneID, target, current, active)
	}
}

func deleteLaunchDesktop(app *testworld.Peer, desktop protocol.Desktop) {
	app.T.Helper()
	req := protocol.DesktopDeleteMessage{Cmd: protocol.CmdDesktopDelete, RequestID: "delete-" + desktop.ID, DesktopID: desktop.ID, ExpectedRevision: desktop.Revision}
	mustProfileRequest(app, req, req.RequestID)
}

func writeLaunchChoice(app *testworld.Peer, kind, id string, setting protocol.LaunchDesktopSetting) protocol.LaunchDesktopItem {
	req := protocol.LaunchDesktopSetMessage{Cmd: protocol.CmdLaunchDesktopSet, RequestID: "choice-" + kind + id, Kind: protocol.LaunchDesktopKind(kind), ItemID: id, Setting: setting}
	result := testworld.Request(app, req, protocol.EventLaunchDesktopResult, func(r protocol.LaunchDesktopResultMessage) bool { return r.RequestID == req.RequestID })
	if !result.Success || result.Item == nil {
		app.T.Fatalf("launch choice: %+v", result)
	}
	return *result.Item
}

func TestNewAutomationCanReadPendingLaunchDestinationsBeforeItExists(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	owner := writeLaunchChoice(app, "crew", "alder", protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopModeOwn, DesktopName: protocol.Ptr("Review")})
	result := testworld.Request(app, protocol.LaunchDesktopGetMessage{Cmd: protocol.CmdLaunchDesktopGet, RequestID: "new-automation", Kind: protocol.LaunchDesktopKindAutomation}, protocol.EventLaunchDesktopResult, func(r protocol.LaunchDesktopResultMessage) bool { return r.RequestID == "new-automation" })
	if !result.Success || result.Item != nil {
		t.Fatalf("catalog = %+v", result)
	}
	for _, item := range result.Items {
		if item.ItemID == owner.ItemID && item.Kind == owner.Kind && protocol.Deref(item.Setting.DestinationID) == protocol.Deref(owner.Setting.DestinationID) && item.Confirmed && protocol.Deref(item.Setting.Pending) {
			return
		}
	}
	t.Fatalf("pending owner absent from catalog: %+v", result.Items)
}

func TestNamedCrewDesktopsShareRecreateAndUserWakeTakesFocus(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	profile := app.SelectedProfile()
	caller := w.Spawn(app, fakeagent.Claude, w.Path("requester"))
	w.Launched(caller)
	focusAgent(t, w, app, caller)
	before := viewProfile(t, w, profile)
	current, active := before.profile.CurrentDesktopID, before.desktops[before.profile.CurrentDesktopID].ActivePaneID
	owner := writeLaunchChoice(app, "crew", "trellis", protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopModeOwn, DesktopName: protocol.Ptr("Review")})
	joined := writeLaunchChoice(app, "crew", "alder", protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopModeDesktop, DestinationID: owner.Setting.DestinationID})
	if joined.Setting.DestinationID == nil || *joined.Setting.DestinationID != *owner.Setting.DestinationID {
		t.Fatal("pending destination not shared")
	}
	writeLaunchChoice(app, "crew", "trellis", protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopModeOwn, DesktopName: protocol.Ptr("Review")})
	inherited := readLaunchSetting(app, "crew", "alder")
	if inherited.Setting.Mode != protocol.LaunchDesktopModeOwn || protocol.Deref(inherited.Setting.OwnerID) != "alder" {
		t.Fatalf("owner switch stranded joiner: %+v", inherited)
	}
	day, err := cli.CrewWake("alder", "", caller)
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(day.SessionID)
	placed, _ := viewProfile(t, w, profile).paneOf(t, day.SessionID)
	if placed.Name != "Review" || protocol.Deref(placed.ShortcutSlot) != 0 {
		t.Fatalf("own desktop took a number: %+v", placed)
	}
	assertBackgroundPlacement(t, w, profile, day.SessionID, placed.ID, current, active)
	event := testworld.Await(app, protocol.EventBackgroundLaunch, func(r protocol.BackgroundLaunchMessage) bool { return r.SessionID == day.SessionID })
	if event.RequestedBy == "" || event.DesktopID != placed.ID || event.Name != "alder" {
		t.Fatalf("arrival: %+v", event)
	}
	other, err := cli.CrewWake("trellis", "", caller)
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(other.SessionID)
	sameName, _ := viewProfile(t, w, profile).paneOf(t, other.SessionID)
	if sameName.Name != placed.Name || sameName.ID == placed.ID {
		t.Fatal("equal names implicitly shared destinations")
	}
	writeLaunchChoice(app, "crew", "trellis", protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopModeOwn, DesktopName: protocol.Ptr("Later")})
	successor := protocol.Deref(crewHandoff(t, cli, other.SessionID, "Continue beside the predecessor.", false, protocol.CrewDayCloseNap).SessionID)
	w.Launched(successor)
	assertBackgroundPlacement(t, w, profile, successor, sameName.ID, current, active)
	napArrival := testworld.Await(app, protocol.EventBackgroundLaunch, func(message protocol.BackgroundLaunchMessage) bool { return message.SessionID == successor })
	if napArrival.DesktopLabel != "Review (no ⌘ number)" || !strings.Contains(napArrival.RequestedBy, "handoff") {
		t.Fatalf("nap arrival names configured rather than actual desktop: %+v", napArrival)
	}
	crewHandoff(t, cli, day.SessionID, "Day complete.", false, protocol.CrewDayCloseSleep)
	awaitClosed(app, day.SessionID)
	if _, exists := viewProfile(t, w, profile).desktops[placed.ID]; exists {
		t.Fatal("empty noncurrent desktop survived")
	}
	again, err := cli.CrewWake("alder", "", caller)
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(again.SessionID)
	recreated, _ := viewProfile(t, w, profile).paneOf(t, again.SessionID)
	if recreated.ID == placed.ID || recreated.Name != "Review" || protocol.Deref(recreated.ShortcutSlot) != 0 {
		t.Fatalf("recreation: %+v", recreated)
	}
	assertBackgroundPlacement(t, w, profile, again.SessionID, recreated.ID, current, active)
	user := wakeCrew(t, cli, "alder", "")
	if !user.AlreadyAwake {
		t.Fatal("user wake started an awake member again")
	}
	show := testworld.Await(app, protocol.EventSessionShowRequested, func(message protocol.SessionShowRequestedMessage) bool { return message.SessionID == again.SessionID })
	if show.SessionID != again.SessionID {
		t.Fatal("shell wake did not ask the app to show the member")
	}
	shown := viewProfile(t, w, profile)
	_, pane := shown.paneOf(t, again.SessionID)
	if shown.profile.CurrentDesktopID != recreated.ID || shown.desktops[recreated.ID].ActivePaneID != pane {
		t.Fatal("user wake did not show the member")
	}
	switchDesktop(app, profile, current)
	woke := testworld.Request(app, protocol.CrewWakeMessage{Cmd: protocol.CmdCrewWake, Member: "alder", RequestID: protocol.Ptr("app-wake")}, protocol.EventCrewWakeResult, func(r protocol.CrewWakeResultMessage) bool { return r.RequestID == "app-wake" })
	if !woke.Success || !protocol.Deref(woke.AlreadyAwake) {
		t.Fatalf("app wake: %+v", woke)
	}
	if got := viewProfile(t, w, profile); got.profile.CurrentDesktopID != recreated.ID || got.desktops[recreated.ID].ActivePaneID != pane {
		t.Fatal("app user wake did not focus the member")
	}

}

func TestAutomationOwnDesktopUsesOnlyExplicitSlotsAndKeepsFocus(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	profile := app.SelectedProfile()
	caller := w.Spawn(app, fakeagent.Claude, w.Path("anchor"))
	w.Launched(caller)
	focusAgent(t, w, app, caller)
	before := viewProfile(t, w, profile)
	current, active := before.profile.CurrentDesktopID, before.desktops[before.profile.CurrentDesktopID].ActivePaneID
	dir := w.Path("check")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, cli, fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nid: nightly\nname: Nightly check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", dir))
	launch := func(key string) string {
		r, err := cli.AutomationRun("nightly", key, "")
		if err != nil {
			t.Fatal(err)
		}
		id := protocol.Deref(r.Run.SessionID)
		w.Launched(id)
		return id
	}
	first := launch("first")
	arrival := testworld.Await(app, protocol.EventBackgroundLaunch, func(event protocol.BackgroundLaunchMessage) bool { return event.SessionID == first })
	if arrival.Name != "Nightly check" || arrival.RequestedBy != "automation" {
		t.Fatalf("automation arrival: %+v", arrival)
	}
	desktop, _ := viewProfile(t, w, profile).paneOf(t, first)
	if protocol.Deref(desktop.ShortcutSlot) != 0 || desktop.Name != "Nightly check" {
		t.Fatalf("default own desktop: %+v", desktop)
	}
	second := launch("second")
	assertBackgroundPlacement(t, w, profile, second, desktop.ID, current, active)
	writeLaunchChoice(app, "automation", "nightly", protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopModeOwn, DesktopName: protocol.Ptr("Checks"), ShortcutSlot: protocol.Ptr(5)})
	third := launch("third")
	numbered, _ := viewProfile(t, w, profile).paneOf(t, third)
	if protocol.Deref(numbered.ShortcutSlot) != 5 {
		t.Fatal("explicit slot was not used")
	}
	closeSession(t, cli, third, "finished")
	awaitClosed(app, third)
	if _, exists := viewProfile(t, w, profile).desktops[numbered.ID]; exists {
		t.Fatal("empty numbered desktop survived")
	}
	occupied := mustProfileRequest(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: "occupy-slot", ProfileID: profile, Name: protocol.Ptr("Taken"), ShortcutSlot: protocol.Ptr(5)}, "occupy-slot").Desktops[0]
	occupant := w.Spawn(app, fakeagent.Claude, w.Path("occupant"))
	w.Launched(occupant)
	switchDesktop(app, profile, current)
	fourth := launch("fourth")
	withoutSlot, _ := viewProfile(t, w, profile).paneOf(t, fourth)
	if withoutSlot.ID == occupied.ID || protocol.Deref(withoutSlot.ShortcutSlot) != 0 || withoutSlot.Name != "Checks" {
		t.Fatalf("occupied slot recreation: %+v", withoutSlot)
	}
	assertBackgroundPlacement(t, w, profile, fourth, withoutSlot.ID, current, active)
	if got := readLaunchSetting(app, "automation", "nightly"); protocol.Deref(got.Setting.ShortcutSlot) != 5 {
		t.Fatal("occupied slot discarded saved preference")
	}
}

func TestReopenReturnsToTheLastDesktopAndFallsBackAfterItIsDeleted(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	profile := app.SelectedProfile()
	anchor := w.Spawn(app, fakeagent.Codex, w.Path("anchor"))
	w.Launched(anchor)
	focusAgent(t, w, app, anchor)
	current, active := placedPane(t, w, anchor)
	session := w.Spawn(app, fakeagent.Codex, w.Path("resume"))
	agent := w.Launched(session)
	app.TypeLine(session, "hello")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=idle -->")
	target := createDesktop(app, profile)
	targetAnchor := w.Spawn(app, fakeagent.Codex, w.Path("target-anchor"))
	w.Launched(targetAnchor)
	switchDesktop(app, profile, current.ID)
	if _, err := cli.MoveSessionToDesktop(session, session, target.ID); err != nil {
		t.Fatal(err)
	}
	closeSession(t, cli, session, "done")
	awaitClosed(app, session)
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: session}); err != nil {
		t.Fatal(err)
	}
	w.Launched(session)
	assertBackgroundPlacement(t, w, profile, session, target.ID, current.ID, active)
	closeSession(t, cli, session, "done again")
	awaitClosed(app, session)
	closeSession(t, cli, targetAnchor, "empty the desktop")
	awaitClosed(app, targetAnchor)
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: session}); err != nil {
		t.Fatal(err)
	}
	w.Launched(session)
	assertBackgroundPlacement(t, w, profile, session, current.ID, current.ID, active)
}

func TestLaunchMigrationNeedsEveryChoiceAndKeepsConfirmedChoicesAcrossRestart(t *testing.T) {
	w := newCrewWorld(t)
	app := w.App()
	read := func(app *testworld.Peer) protocol.MigrationState {
		t.Helper()
		result := testworld.Request(app, protocol.MigrationGetMessage{Cmd: protocol.CmdMigrationGet, RequestID: "migration"}, protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "migration" })
		if !result.Success || result.State == nil {
			t.Fatalf("migration_get = %+v", result)
		}
		return *result.State
	}
	finish := func(app *testworld.Peer, state protocol.MigrationState) protocol.MigrationResultMessage {
		t.Helper()
		return testworld.Request(app, protocol.MigrationFinishMessage{Cmd: protocol.CmdMigrationFinish, RequestID: "finish", ExpectedRevision: state.Revision}, protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "finish" })
	}
	state := read(app)
	if state.Phase != protocol.MigrationPhaseLaunchRequired || len(state.Groups) != 0 || len(state.LaunchItems) != 3 {
		t.Fatalf("migration without workspaces = %+v", state)
	}
	for _, item := range state.LaunchItems {
		if item.Confirmed {
			t.Fatal("startup silently confirmed a suggestion")
		}
	}
	target := createDesktop(app, app.SelectedProfile())
	launchSetting(app, "crew", "alder", "desktop", target.ID)
	if result := finish(app, state); result.Success {
		t.Fatal("stale launch review finished migration")
	}
	w.restart()
	app = w.App()
	state = read(app)
	for _, item := range state.LaunchItems {
		if item.ItemID == "alder" {
			if !item.Confirmed || protocol.Deref(item.Setting.DesktopID) != target.ID {
				t.Fatalf("restart lost the choice: %+v", item)
			}
		} else {
			if item.Confirmed || item.Setting.Mode != protocol.LaunchDesktopModeOwn {
				t.Fatalf("default was silently confirmed: %+v", item)
			}
		}
	}
	stale := read(app)
	writeLaunchChoice(app, "crew", "alder", protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopModeOwn, DesktopName: protocol.Ptr("Changed")})
	if result := finish(app, stale); result.Success {
		t.Fatal("a deleted launch destination did not invalidate the review")
	}
	done := finish(app, read(app))
	if !done.Success || done.State.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("confirmed finish = %+v", done)
	}
	w.restart()
	if got := read(w.App()); got.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("completed review returned: %+v", got)
	}
}

func TestUpgradingAnInstallWithOnlyAutomationsRequiresLaunchReviewAndPlacesExistingAgents(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	dir := w.Path("check")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, canonical, err := automation.ParseDefinitionYAML([]byte(fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nid: check\nname: Local check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", dir)))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenDBAtSchemaVersion(filepath.Join(w.Dir, "attn.db"), 166)
	if err != nil {
		t.Fatal(err)
	}
	var profileID string
	if err := db.QueryRow(`SELECT id FROM profiles WHERE deleted_at = '' LIMIT 1`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO automation_definitions(id, name, enabled, revision, spec_json, created_at, updated_at, profile_id) VALUES ('check', 'Local check', 1, 1, ?, 'now', 'now', ?);
 INSERT INTO sessions(id, label, directory, state_since, state_updated_at, last_seen, profile_id, agent, launch_intent) VALUES ('existing', 'Existing agent', '/fixture', 'now', 'now', 'now', ?, 'shell', '{}');`, string(canonical), profileID, profileID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	w.start()
	app := w.App()
	result := testworld.Request(app, protocol.MigrationGetMessage{Cmd: protocol.CmdMigrationGet, RequestID: "migration"}, protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "migration" })
	if !result.Success || result.State == nil || result.State.Phase != protocol.MigrationPhaseLaunchRequired || len(result.State.LaunchItems) != 1 {
		t.Fatalf("automation-only migration = %+v", result)
	}
	item := result.State.LaunchItems[0]
	if item.Kind != protocol.LaunchDesktopKindAutomation || item.ItemID != "check" || item.Confirmed || item.Setting.Mode != protocol.LaunchDesktopModeOwn {
		t.Fatalf("existing automation = %+v", item)
	}
	viewProfile(t, w, profileID).paneOf(t, "existing")
	run, err := w.Client().AutomationRun("check", "during-review", "")
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(protocol.Deref(run.Run.SessionID))
	oldRevision := result.State.Revision
	changed := testworld.Request(app, protocol.MigrationGetMessage{Cmd: protocol.CmdMigrationGet, RequestID: "changed"}, protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "changed" })
	if !changed.Success || changed.State.Revision <= oldRevision || protocol.Deref(changed.State.LaunchItems[0].Setting.Pending) || changed.State.LaunchItems[0].Confirmed {
		t.Fatalf("dedicated launch did not version pending choice: %+v", changed)
	}
	stale := testworld.Request(app, protocol.MigrationFinishMessage{Cmd: protocol.CmdMigrationFinish, RequestID: "stale", ExpectedRevision: oldRevision}, protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "stale" })
	if stale.Success {
		t.Fatal("an unseen dedicated desktop was confirmed")
	}
	result = changed
	finished := testworld.Request(app, protocol.MigrationFinishMessage{Cmd: protocol.CmdMigrationFinish, RequestID: "finish", ExpectedRevision: result.State.Revision}, protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "finish" })
	if !finished.Success || finished.State.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("accept suggestions = %+v", finished)
	}
	if got := readLaunchSetting(app, "automation", "check"); !got.Confirmed {
		t.Fatal("finish did not confirm automation")
	}
}

func TestALaunchWhoseDesktopDisappearsDuringDriverPreparationLeavesNoAgentOrActiveRun(t *testing.T) {
	w := newWorld(t)
	app, control := w.App(), w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "resume": true})
	awaitDriverAvailable(app, "snipe")
	w.Spawn(app, shellHarness, w.Path("keep-source"))
	target := createDesktop(control, control.SelectedProfile())
	dir := w.Path("launch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	driver.holdLaunch = true
	driver.mu.Unlock()
	request := protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: "missing-destination", Cwd: dir, Agent: "snipe", ProfileID: app.SelectedProfile(), Placement: &protocol.SessionPlacement{DesktopID: protocol.Ptr(target.ID)}, Cols: 80, Rows: 24}
	app.Send(request)
	var launch driverLaunch
	reply := driver.asked("driver.spawn", &launch)
	deleteLaunchDesktop(control, target)
	driver.answer(reply, map[string]any{"argv": []string{"/bin/cat"}})
	driver.launched()
	result := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == request.ID })
	if result.Success || !strings.Contains(protocol.Deref(result.Error), target.ID) {
		t.Fatalf("vanished destination = %+v", result)
	}
	if closed := driver.closed(); closed.Reason != "launch_failed" || closed.RunID != launch.RunID {
		t.Fatalf("driver close = %+v", closed)
	}
	fresh := w.App()
	for _, session := range fresh.Initial.Sessions {
		if session.ID == request.ID {
			t.Fatalf("failed launch retained agent: %+v", session)
		}
	}
	for _, desktop := range fresh.Initial.Desktops {
		for _, pane := range desktop.Panes {
			if pane.SessionID == request.ID {
				t.Fatal("failed launch retained a pane")
			}
		}
	}
	driver.register("snipe", map[string]bool{"state_reporting": true, "resume": true})
	for _, run := range driver.registered.ActiveRuns {
		if run.SessionID == request.ID {
			t.Fatal("failed launch retained an active driver run")
		}
	}
	driver.mu.Lock()
	driver.holdLaunch = false
	driver.mu.Unlock()
	retry, _ := spawnDriven(w, fresh, driver, dir, func(m *protocol.SpawnSessionMessage) { m.ID = request.ID })
	if retry != request.ID {
		t.Fatal("retry changed session identity")
	}
}

func TestMovingAnAgentBetweenProfilesPlacesItWithoutTakingFocus(t *testing.T) {
	w := &world{World: prepareWorld(t), terms: testworld.NewTerminals()}
	w.start()
	app := w.App()
	source := app.SelectedProfile()
	moving := w.Spawn(app, fakeagent.Claude, w.Path("moving"))
	destination := createProfile(app, "Destination")
	app = w.AppOn(destination.ID)
	w.Spawn(app, fakeagent.Claude, w.Path("destination-anchor"))
	before := viewProfile(t, w, destination.ID)
	current := before.profile.CurrentDesktopID
	active := before.desktops[current].ActivePaneID
	observer := w.AppOn(destination.ID)
	result := mustProfileRequest(app, protocol.SessionMoveMessage{Cmd: protocol.CmdSessionMove, RequestID: "move", SessionID: moving, ExpectedProfileID: source, DestinationProfileID: destination.ID}, "move")
	var returned bool
	for _, desktop := range result.Desktops {
		for _, pane := range desktop.Panes {
			if pane.SessionID == moving {
				returned = true
			}
		}
	}
	if !returned {
		t.Fatal("move result omitted destination placement")
	}
	testworld.Await(observer, protocol.EventProfileArrangementChanged, func(r protocol.ProfileArrangementChangedMessage) bool {
		for _, desktop := range r.Desktops {
			for _, pane := range desktop.Panes {
				if pane.SessionID == moving {
					return true
				}
			}
		}
		return false
	})
	assertBackgroundPlacement(t, w, destination.ID, moving, current, current, active)
	w.restart()
	assertBackgroundPlacement(t, w, destination.ID, moving, current, current, active)
}

func TestAdoptingAnOrphanRuntimePlacesItOnTheCurrentDesktop(t *testing.T) {
	w := &world{World: prepareWorld(t), terms: testworld.NewTerminals()}
	w.start()
	app := w.App()
	profile := app.SelectedProfile()
	anchor := w.Spawn(app, fakeagent.Claude, w.Path("anchor"))
	focusAgent(t, w, app, anchor)
	before := viewProfile(t, w, profile)
	current := before.profile.CurrentDesktopID
	active := before.desktops[current].ActivePaneID
	w.stop()
	if err := w.terms.Spawn(context.Background(), ptybackend.SpawnOptions{ID: "orphan-runtime", Agent: "claude", CWD: w.Path("orphan"), Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	w.start()
	assertBackgroundPlacement(t, w, profile, "orphan-runtime", current, current, active)
	w.restart()
	assertBackgroundPlacement(t, w, profile, "orphan-runtime", current, current, active)
}

func TestFinishingMigrationWithoutLaunchItemsDoesNotReopenForALaterAutomation(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	directory := w.Path("check")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	definition := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nid: check\nname: Local check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", directory)
	_, canonical, err := automation.ParseDefinitionYAML([]byte(definition))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenDBAtSchemaVersion(filepath.Join(w.Dir, "attn.db"), 166)
	if err != nil {
		t.Fatal(err)
	}
	var profileID string
	if err := db.QueryRow(`SELECT id FROM profiles WHERE deleted_at = '' LIMIT 1`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	manifest := profilemigration.Manifest{ProfileID: profileID}
	imported, err := profilemigration.EncodeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := profilemigration.EncodePlan(profilemigration.InitialPlan(manifest))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE profile_migration SET phase = 'placement_required', imported_groups = ?, draft = ? WHERE id = 1;
 INSERT INTO automation_definitions(id,name,enabled,revision,spec_json,created_at,updated_at,profile_id) VALUES ('check','Local check',1,1,?,'now','now',?);`, imported, plan, string(canonical), profileID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	w.start()
	app, cli := w.App(), w.Client()
	read := func(app *testworld.Peer) protocol.MigrationState {
		result := testworld.Request(app, protocol.MigrationGetMessage{Cmd: protocol.CmdMigrationGet, RequestID: "read"}, protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "read" })
		if !result.Success || result.State == nil {
			t.Fatalf("migration read: %+v", result)
		}
		return *result.State
	}
	state := read(app)
	if state.Phase != protocol.MigrationPhasePlacementRequired {
		t.Fatalf("initial phase: %s", state.Phase)
	}
	if err := cli.AutomationDelete("check"); err != nil {
		t.Fatal(err)
	}
	state = read(app)
	done := testworld.Request(app, protocol.MigrationFinishMessage{Cmd: protocol.CmdMigrationFinish, RequestID: "finish", ExpectedRevision: state.Revision}, protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "finish" })
	if !done.Success || done.State == nil || done.State.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("finish: %+v", done)
	}
	applyAutomation(t, cli, strings.Replace(definition, "id: check", "id: later", 1))
	w.restart()
	if got := read(w.App()); got.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("later automation reopened migration: %+v", got)
	}
}

func TestRestoringASharedAutomationOwnerInAnotherProfileKeepsDestinationsIndependent(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	original := app.SelectedProfile()
	dir := w.Path("check")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nid: shared-owner\nname: Shared check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", dir)
	applyAutomation(t, cli, spec)
	owner := readLaunchSetting(app, "automation", "shared-owner")
	writeLaunchChoice(app, "crew", "alder", protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopModeDesktop, DestinationID: owner.Setting.DestinationID})
	if err := cli.AutomationDelete("shared-owner"); err != nil {
		t.Fatal(err)
	}
	destination := createProfile(app, "Other")
	otherApp := w.AppOn(destination.ID)
	req := protocol.AutomationApplyMessage{Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr("restore"), DefinitionYaml: spec, ProfileID: protocol.Ptr(destination.ID)}
	restored := testworld.Request(otherApp, req, protocol.EventAutomationApplyResult, func(r protocol.AutomationApplyResultMessage) bool { return protocol.Deref(r.RequestID) == "restore" })
	if !restored.Success {
		t.Fatalf("restore: %+v", restored)
	}
	fresh := readLaunchSetting(otherApp, "automation", "shared-owner")
	retained := readLaunchSetting(app, "crew", "alder")
	if protocol.Deref(fresh.Setting.DestinationID) == protocol.Deref(retained.Setting.DestinationID) {
		t.Fatal("restored owner rejoined its former shared destination")
	}
	run, err := cli.AutomationRun("shared-owner", "restored", "")
	if err != nil {
		t.Fatal(err)
	}
	runID := protocol.Deref(run.Run.SessionID)
	w.Launched(runID)
	viewProfile(t, w, destination.ID).paneOf(t, runID)
	arrival := testworld.Await(app, protocol.EventBackgroundLaunch, func(message protocol.BackgroundLaunchMessage) bool { return message.SessionID == runID })
	if arrival.ProfileID != destination.ID {
		t.Fatalf("cross-profile arrival identifies %s, want %s", arrival.ProfileID, destination.ID)
	}
	wake, err := cli.CrewWake("alder", "", "")
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(wake.SessionID)
	viewProfile(t, w, original).paneOf(t, wake.SessionID)
}

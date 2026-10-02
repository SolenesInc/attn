package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
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

func TestCrewLaunchDesktopPreservesFocusAndFallsBackAfterDeletion(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	profile := app.SelectedProfile()
	target := createDesktop(app, profile)
	launchSetting(app, "crew", "trellis", "desktop", target.ID)
	w.restart()
	app = w.App()
	cli := w.Client()
	if got := readLaunchSetting(app, "crew", "trellis"); protocol.Deref(got.Setting.DesktopID) != target.ID {
		t.Fatalf("restart lost destination: %+v", got)
	}
	occupied := w.Spawn(app, fakeagent.Claude, w.Path("target-anchor"))
	w.Launched(occupied)
	if _, err := cli.MoveSessionToDesktop(occupied, occupied, target.ID); err != nil {
		t.Fatal(err)
	}
	anchor := w.Spawn(app, fakeagent.Claude, w.Path("anchor"))
	w.Launched(anchor)
	focusAgent(t, w, app, anchor)
	before := viewProfile(t, w, profile)
	current := before.profile.CurrentDesktopID
	active := before.desktops[current].ActivePaneID
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(day.SessionID)
	assertBackgroundPlacement(t, w, profile, day.SessionID, target.ID, current, active)
	targetActive := viewProfile(t, w, profile).desktops[target.ID].ActivePaneID
	successor := protocol.Deref(crewHandoff(t, cli, day.SessionID, "Continue on the chosen desktop.", false, protocol.CrewDayCloseNap).SessionID)
	next := w.Launched(successor)
	assertBackgroundPlacement(t, w, profile, successor, target.ID, current, active)
	if got := viewProfile(t, w, profile).desktops[target.ID].ActivePaneID; got != targetActive {
		t.Fatalf("nap changed target active pane: %s, want %s", got, targetActive)
	}
	day.SessionID = successor
	deleteLaunchDesktop(app, viewProfile(t, w, profile).desktops[target.ID])
	assertBackgroundPlacement(t, w, profile, day.SessionID, current, current, active)
	setting := readLaunchSetting(app, "crew", "trellis")
	if !protocol.Deref(setting.Setting.Fallback) || protocol.Deref(setting.Setting.DesktopID) != target.ID || !strings.Contains(protocol.Deref(setting.Setting.Label), fmt.Sprintf("Desktop %d", protocol.Deref(target.ShortcutSlot))) {
		t.Fatalf("deleted setting = %+v", setting)
	}
	launchSetting(app, "crew", "alder", "desktop", current)
	second := wakeCrew(t, cli, "alder", "")
	w.Launched(second.SessionID)
	assertBackgroundPlacement(t, w, profile, second.SessionID, current, current, active)
	exitCrewDay(w, next)
	fallback := wakeCrew(t, cli, "trellis", "")
	w.Launched(fallback.SessionID)
	assertBackgroundPlacement(t, w, profile, fallback.SessionID, current, current, active)
}

func TestAutomationLaunchDesktopIsCreatedOnceAndCanBeChanged(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, cli, fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
id: nightly
name: Nightly check
trigger: {type: manual}
prompt: Check locally.
launch: {driver: claude}
location: {type: directory, path: %q}
`, w.Path("check")))
	profile := app.SelectedProfile()
	anchor := w.Spawn(app, fakeagent.Claude, w.Path("anchor"))
	w.Launched(anchor)
	focusAgent(t, w, app, anchor)
	before := viewProfile(t, w, profile)
	current, active := before.profile.CurrentDesktopID, before.desktops[before.profile.CurrentDesktopID].ActivePaneID
	run := func(id string) string {
		t.Helper()
		result, err := cli.AutomationRun("nightly", id, "")
		if err != nil {
			t.Fatal(err)
		}
		session := protocol.Deref(result.Run.SessionID)
		w.Launched(session)
		return session
	}
	first := run("first")
	desktop, _ := viewProfile(t, w, profile).paneOf(t, first)
	if desktop.Name != "Nightly check" || desktop.ID == current {
		t.Fatalf("dedicated desktop = %+v", desktop)
	}
	assertBackgroundPlacement(t, w, profile, first, desktop.ID, current, active)
	firstActive := viewProfile(t, w, profile).desktops[desktop.ID].ActivePaneID
	second := run("second")
	if got := viewProfile(t, w, profile).desktops[desktop.ID].ActivePaneID; got != firstActive {
		t.Fatalf("automation changed target active pane: %s, want %s", got, firstActive)
	}
	assertBackgroundPlacement(t, w, profile, second, desktop.ID, current, active)
	if len(viewProfile(t, w, profile).desktops) != len(before.desktops)+1 {
		t.Fatal("the second run created another desktop")
	}
	deleteLaunchDesktop(app, viewProfile(t, w, profile).desktops[desktop.ID])
	if !protocol.Deref(readLaunchSetting(app, "automation", "nightly").Setting.Fallback) {
		t.Fatal("missing desktop fallback is not visible")
	}
	third := run("third")
	assertBackgroundPlacement(t, w, profile, third, current, current, active)
	target := createDesktop(app, profile)
	launchSetting(app, "automation", "nightly", "desktop", target.ID)
	fourth := run("fourth")
	assertBackgroundPlacement(t, w, profile, fourth, target.ID, current, active)
	launchSetting(app, "automation", "nightly", "current", "")
	fifth := run("fifth")
	assertBackgroundPlacement(t, w, profile, fifth, current, current, active)
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
	deleteLaunchDesktop(app, viewProfile(t, w, profile).desktops[target.ID])
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
			if item.Confirmed || item.Setting.Mode != protocol.LaunchDesktopModeCurrent {
				t.Fatalf("default was silently confirmed: %+v", item)
			}
		}
	}
	stale := read(app)
	deleteLaunchDesktop(app, viewProfile(t, w, app.SelectedProfile()).desktops[target.ID])
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
	if item.Kind != protocol.LaunchDesktopKindAutomation || item.ItemID != "check" || item.Confirmed || item.Setting.Mode != protocol.LaunchDesktopModeDedicated {
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
	if !changed.Success || changed.State.Revision <= oldRevision || changed.State.LaunchItems[0].Setting.Mode != protocol.LaunchDesktopModeDesktop || changed.State.LaunchItems[0].Confirmed {
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

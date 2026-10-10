package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/profilemigration"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func readLaunchSetting(app *testworld.Peer, kind, id string) protocol.LaunchDesktopItem {
	app.T.Helper()
	req := protocol.LaunchDesktopGetMessage{Cmd: protocol.CmdLaunchDesktopGet, RequestID: "get-" + kind + id, Kind: protocol.LaunchDesktopKind(kind), ItemID: id}
	result := testworld.Request(app, req, protocol.EventLaunchDesktopResult, func(r protocol.LaunchDesktopResultMessage) bool { return r.RequestID == req.RequestID })
	if !result.Success || result.Item == nil {
		app.T.Fatalf("read launch setting: %+v", result)
	}
	return *result.Item
}

// awaitDesktopRemoved waits, on a client that saw the desktop, for the arrangement without it.

// awaitDesktopRemoved waits, on a client that saw the desktop, for the arrangement without it.
func awaitDesktopRemoved(t *testing.T, w *world, profileID, desktopID string) {
	t.Helper()
	observer := w.AppOn(profileID)
	defer observer.Close()
	gone := func(desktops []protocol.Desktop) bool {
		return !slices.ContainsFunc(desktops, func(d protocol.Desktop) bool { return d.ID == desktopID })
	}
	if gone(observer.Initial.Desktops) {
		return
	}
	testworld.Await(observer, protocol.EventProfileArrangementChanged, func(m protocol.ProfileArrangementChangedMessage) bool {
		return m.Profile.ID == profileID && gone(m.Desktops)
	})
}

func assertBackgroundPlacement(t *testing.T, w *world, profileID, sessionID, target, current, active string) {
	t.Helper()
	view := viewProfile(t, w, profileID)
	desktop, _ := view.paneOf(t, sessionID)
	if desktop.ID != target || view.profile.CurrentDesktopID != current || view.desktops[current].ActivePaneID != active {
		t.Fatalf("launch %s placed on %s, current=%s active=%s; want target=%s current=%s active=%s", sessionID, desktop.ID, view.profile.CurrentDesktopID, view.desktops[current].ActivePaneID, target, current, active)
	}
}

func writeLaunchChoice(app *testworld.Peer, kind, id string, setting protocol.LaunchDesktopSetting) protocol.LaunchDesktopItem {
	req := protocol.LaunchDesktopSetMessage{Cmd: protocol.CmdLaunchDesktopSet, RequestID: "choice-" + kind + id, Kind: protocol.LaunchDesktopKind(kind), ItemID: id, Setting: &setting}
	result := testworld.Request(app, req, protocol.EventLaunchDesktopResult, func(r protocol.LaunchDesktopResultMessage) bool { return r.RequestID == req.RequestID })
	if !result.Success || result.Item == nil {
		app.T.Fatalf("launch choice: %+v", result)
	}
	return *result.Item
}

func TestANewDesktopChoiceExistsAtOnceAndItemsChoosingItShareIt(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	profile := app.SelectedProfile()
	caller := w.Spawn(app, fakeagent.Claude, w.Path("requester"))
	w.Launched(caller)
	focusAgent(t, w, app, caller)
	before := viewProfile(t, w, profile)
	current, active := before.profile.CurrentDesktopID, before.desktops[before.profile.CurrentDesktopID].ActivePaneID
	slot5 := profile + "/desktop_5"
	chosen := writeLaunchChoice(app, "crew", "trellis", protocol.LaunchDesktopSetting{DesktopName: protocol.Ptr("Review"), DesktopID: protocol.Ptr(slot5)})
	if protocol.Deref(chosen.Setting.DesktopID) != slot5 || protocol.Deref(chosen.Setting.Label) != "5 · Review" || !chosen.Confirmed {
		t.Fatalf("new desktop on an empty slot: %+v", chosen)
	}
	if created, exists := viewProfile(t, w, profile).desktops[slot5]; !exists || created.Name != "Review" || len(created.Panes) != 0 {
		t.Fatalf("the chosen desktop was not created empty and named: %+v", created)
	}
	if shared := writeLaunchChoice(app, "crew", "alder", protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(slot5)}); protocol.Deref(shared.Setting.Label) != "5 · Review" {
		t.Fatalf("shared choice: %+v", shared)
	}
	for _, member := range []string{"trellis", "alder"} {
		day, err := cli.CrewWake(member, "", protocol.SessionID(caller))
		if err != nil {
			t.Fatal(err)
		}
		w.Launched(string(day.SessionID))
		assertBackgroundPlacement(t, w, profile, string(day.SessionID), slot5, current, active)
		arrival := testworld.Await(app, protocol.EventBackgroundLaunch, func(r protocol.BackgroundLaunchMessage) bool { return r.SessionID == day.SessionID })
		if arrival.Name != strings.ToUpper(member[:1])+member[1:] || arrival.DesktopLabel != "5 · Review" || arrival.RequestedBy == "" {
			t.Fatalf("arrival: %+v", arrival)
		}
	}
	desktop := viewProfile(t, w, profile).desktops[slot5]
	mustProfileRequest(app, protocol.DesktopRenameMessage{Cmd: protocol.CmdDesktopRename, RequestID: "rename", DesktopID: slot5, Name: "Renamed", ExpectedRevision: desktop.Revision}, "rename")
	testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
		renamed := 0
		for _, m := range e.Members {
			if (m.Key == "alder" || m.Key == "trellis") && m.LaunchDesktop != nil && protocol.Deref(m.LaunchDesktop.Label) == "5 · Renamed" {
				renamed++
			}
		}
		return renamed == 2
	})
}

func TestADesktopAnItemStartsOnIsNeverRemovedWhileItDoes(t *testing.T) {
	inCrewBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profile := app.SelectedProfile()
		tied := createDesktop(app, profile)
		writeLaunchChoice(app, "crew", "alder", protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(tied.ID)})
		other := createDesktop(app, profile)
		w.advance(time.Hour)
		if _, exists := viewProfile(t, w, profile).desktops[tied.ID]; !exists {
			t.Fatal("an empty desktop a crew member starts on was removed")
		}
		writeLaunchChoice(app, "crew", "alder", protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(other.ID)})
		w.advance(29 * time.Second)
		if _, exists := viewProfile(t, w, profile).desktops[tied.ID]; !exists {
			t.Fatal("a desktop untied 29s ago was removed")
		}
		w.advance(time.Second)
		awaitDesktopRemoved(t, w, profile, tied.ID)
	})
}

func TestANewAutomationStartsOnItsOwnNamedDesktopWithoutTakingFocus(t *testing.T) {
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
	spec := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nname: Nightly check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", dir)
	defined := applyAutomation(t, cli, spec)
	own := protocol.Deref(defined.LaunchDesktop.DesktopID)
	if desktop, exists := viewProfile(t, w, profile).desktops[own]; !exists || desktop.Name != "Nightly check" || desktop.ShortcutSlot != nil || protocol.Deref(defined.LaunchDesktop.Label) != "Nightly check (no ⌘ number)" {
		t.Fatalf("default desktop %+v for %+v", desktop, defined.LaunchDesktop)
	}
	run, err := cli.AutomationRun(defined.ID, "first", "")
	if err != nil {
		t.Fatal(err)
	}
	session := protocol.Deref(run.Run.SessionID)
	w.Launched(string(session))
	assertBackgroundPlacement(t, w, profile, string(session), own, current, active)
	arrival := testworld.Await(app, protocol.EventBackgroundLaunch, func(event protocol.BackgroundLaunchMessage) bool { return event.SessionID == session })
	if arrival.Name != "Nightly check" || arrival.RequestedBy != "automation" || arrival.DesktopLabel != "Nightly check (no ⌘ number)" {
		t.Fatalf("automation arrival: %+v", arrival)
	}
}

func TestTheLaunchReviewCreatesSuggestedDesktopsOnFinishAndKeepsChoicesAcrossRestart(t *testing.T) {
	w := newCrewWorld(t)
	app := w.App()
	profile := app.SelectedProfile()
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
		if item.Confirmed || item.Setting.DesktopID != nil || protocol.Deref(item.Setting.Label) != item.Name+" (new)" {
			t.Fatalf("suggestion is not a desktop Finish creates: %+v", item)
		}
	}
	target := createDesktop(app, profile)
	writeLaunchChoice(app, "crew", "alder", protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(target.ID)})
	if result := finish(app, state); result.Success {
		t.Fatal("stale launch review finished migration")
	}
	w.restart()
	app = w.App()
	state = read(app)
	for _, item := range state.LaunchItems {
		if chosen := item.ItemID == "alder"; chosen != item.Confirmed || chosen != (protocol.Deref(item.Setting.DesktopID) == target.ID) {
			t.Fatalf("restart changed the review: %+v", item)
		}
	}
	for _, desktop := range viewProfile(t, w, profile).desktops {
		if desktop.Name != "" {
			t.Fatalf("the review created %+v before Finish", desktop)
		}
	}
	done := finish(app, state)
	if !done.Success || done.State.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("finish = %+v", done)
	}
	view := viewProfile(t, w, profile)
	for _, member := range []string{"alder", "keel", "trellis"} {
		item := readLaunchSetting(app, "crew", member)
		desktop, exists := view.desktops[protocol.Deref(item.Setting.DesktopID)]
		if !item.Confirmed || !exists || (member == "alder") != (desktop.ID == target.ID) || (member != "alder" && desktop.Name != strings.ToUpper(member[:1])+member[1:]) {
			t.Fatalf("%s after finish: %+v on %+v", member, item, desktop)
		}
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
	_, canonical, err := automation.ParseDefinitionYAML([]byte(fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nname: Local check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", dir)))
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
	if _, err := db.Exec(`INSERT INTO automation_definitions(id, name, enabled, revision, spec_json, created_at, updated_at, profile_id) VALUES (1, 'Local check', 1, 1, json_set(?, '$.id', 1), 'now', 'now', ?);
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
	if item.Kind != protocol.LaunchDesktopKindAutomation || item.ItemID != "1" || item.Confirmed || protocol.Deref(item.Setting.Label) != "Local check (new)" {
		t.Fatalf("existing automation = %+v", item)
	}
	current, active := placedPane(t, w, "existing")
	run, err := w.Client().AutomationRun(1, "during-review", "")
	if err != nil {
		t.Fatal(err)
	}
	during := protocol.Deref(run.Run.SessionID)
	w.Launched(string(during))
	assertBackgroundPlacement(t, w, profileID, string(during), current.ID, current.ID, active)
	finished := testworld.Request(app, protocol.MigrationFinishMessage{Cmd: protocol.CmdMigrationFinish, RequestID: "finish", ExpectedRevision: result.State.Revision}, protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "finish" })
	if !finished.Success || finished.State.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("accept suggestions = %+v", finished)
	}
	got := readLaunchSetting(app, "automation", "1")
	if !got.Confirmed || protocol.Deref(got.Setting.Label) != "Local check (no ⌘ number)" {
		t.Fatalf("finish did not give the automation its desktop: %+v", got)
	}
}

func TestReopenReturnsToItsNumberedDesktopEvenAfterItWasRemoved(t *testing.T) {
	t.Setenv("ATTN_EMPTY_DESKTOP_GRACE", "0")
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
	if target.ID != fmt.Sprintf("%s/desktop_%d", profile, protocol.Deref(target.ShortcutSlot)) {
		t.Fatalf("a desktop on ⌘%d has id %s", protocol.Deref(target.ShortcutSlot), target.ID)
	}
	targetAnchor := w.Spawn(app, fakeagent.Codex, w.Path("target-anchor"))
	w.Launched(targetAnchor)
	switchDesktop(app, profile, current.ID)
	if _, err := cli.MoveSessionToDesktop(protocol.SessionID(session), protocol.SessionID(session), target.ID); err != nil {
		t.Fatal(err)
	}
	closeSession(t, cli, session, "done")
	awaitClosed(app, session)
	closeSession(t, cli, targetAnchor, "empty the desktop")
	awaitClosed(app, targetAnchor)
	awaitDesktopRemoved(t, w, profile, target.ID)
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(session)}); err != nil {
		t.Fatal(err)
	}
	w.Launched(session)
	assertBackgroundPlacement(t, w, profile, session, target.ID, current.ID, active)
}

func TestANamedDesktopIsNeverRemovedUntilItsNameIsCleared(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profile := app.SelectedProfile()
		named := createDesktop(app, profile)
		mustProfileRequest(app, protocol.DesktopRenameMessage{Cmd: protocol.CmdDesktopRename, RequestID: "name", DesktopID: named.ID, Name: "Notes", ExpectedRevision: named.Revision}, "name")
		createDesktop(app, profile)
		w.advance(time.Hour)
		kept, exists := viewProfile(t, w, profile).desktops[named.ID]
		if !exists {
			t.Fatal("an empty named desktop was removed")
		}
		mustProfileRequest(app, protocol.DesktopRenameMessage{Cmd: protocol.CmdDesktopRename, RequestID: "unname", DesktopID: named.ID, Name: "", ExpectedRevision: kept.Revision}, "unname")
		w.advance(29 * time.Second)
		if _, exists := viewProfile(t, w, profile).desktops[named.ID]; !exists {
			t.Fatal("a desktop unnamed 29s ago was removed")
		}
		w.advance(time.Second)
		testworld.Await(app, protocol.EventProfileArrangementChanged, func(m protocol.ProfileArrangementChangedMessage) bool {
			return !slices.ContainsFunc(m.Desktops, func(d protocol.Desktop) bool { return d.ID == named.ID })
		})
	})
}

func TestAnEmptyDesktopTheUserLeftIsRemovedThirtySecondsLaterAndTheCurrentOneNever(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profile := app.SelectedProfile()
		left := viewProfile(t, w, profile).profile.CurrentDesktopID
		shown := createDesktop(app, profile)
		w.advance(29 * time.Second)
		if _, exists := viewProfile(t, w, profile).desktops[left]; !exists {
			t.Fatal("an empty desktop the user left 29s ago was removed")
		}
		w.advance(time.Second)
		testworld.Await(app, protocol.EventProfileArrangementChanged, func(m protocol.ProfileArrangementChangedMessage) bool {
			return !slices.ContainsFunc(m.Desktops, func(d protocol.Desktop) bool { return d.ID == left })
		})
		w.advance(time.Hour)
		view := viewProfile(t, w, profile)
		if _, exists := view.desktops[left]; exists || len(view.desktops) != len(app.Initial.Desktops) || view.profile.CurrentDesktopID != shown.ID {
			t.Fatalf("after an hour the profile holds %v with %s current, want the protected Chief office and the empty current desktop %s", view.desktops, view.profile.CurrentDesktopID, shown.ID)
		}
	})
}

func TestALaunchWhoseNumberedDesktopWasRemovedRecreatesItWithoutFocus(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		profile := app.SelectedProfile()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
		awaitDriverAvailable(app, "snipe")
		dir := w.Path("launch")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		anchor, _ := spawnDriven(w, app, driver, dir)
		current, active := placedPane(t, w, anchor)
		target := createDesktop(app, profile)
		switchDesktop(app, profile, current.ID)
		w.advance(20 * time.Second)

		driver.mu.Lock()
		driver.holdLaunch = true
		driver.mu.Unlock()
		request := protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: "late-launch", Cwd: dir, Agent: "snipe", ProfileID: profile, Placement: &protocol.SessionPlacement{DesktopID: protocol.Ptr(target.ID)}, Cols: 80, Rows: 24}
		app.Send(request)
		reply := driver.asked("driver.spawn", nil)
		w.advance(10 * time.Second)
		if _, exists := viewProfile(t, w, profile).desktops[target.ID]; exists {
			t.Fatal("the launch's empty target desktop outlived 30s")
		}
		driver.answer(reply, map[string]any{"argv": []string{"/bin/cat"}})
		driver.launched()
		if result := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == request.ID }); !result.Success {
			t.Fatalf("a launch whose desktop vanished = %+v", result)
		}
		assertBackgroundPlacement(t, w, profile, string(request.ID), target.ID, current.ID, active)

		driver.mu.Lock()
		driver.holdLaunch = false
		driver.mu.Unlock()
		later, _ := spawnDriven(w, app, driver, dir, func(m *protocol.SpawnSessionMessage) {
			m.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(target.ID)}
		})
		assertBackgroundPlacement(t, w, profile, later, target.ID, current.ID, active)
	})
}

func TestFinishingMigrationWithoutLaunchItemsDoesNotReopenForALaterAutomation(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	directory := w.Path("check")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	definition := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nname: Local check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", directory)
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
 INSERT INTO automation_definitions(id,name,enabled,revision,spec_json,created_at,updated_at,profile_id) VALUES (1,'Local check',1,1,json_set(?, '$.id', 1),'now','now',?);`, imported, plan, string(canonical), profileID); err != nil {
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
	if err := cli.AutomationDelete(1); err != nil {
		t.Fatal(err)
	}
	state = read(app)
	done := testworld.Request(app, protocol.MigrationFinishMessage{Cmd: protocol.CmdMigrationFinish, RequestID: "finish", ExpectedRevision: state.Revision}, protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "finish" })
	if !done.Success || done.State == nil || done.State.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("finish: %+v", done)
	}
	applyAutomation(t, cli, definition)
	w.restart()
	if got := read(w.App()); got.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("later automation reopened migration: %+v", got)
	}
}

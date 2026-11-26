package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
		day, err := crewStartFrom(t, cli, member, protocol.SessionID(caller))
		if err != nil {
			t.Fatal(err)
		}
		w.Launched(string(day.SessionID))
		assertBackgroundPlacement(t, w, profile, string(day.SessionID), slot5, current, active)
	}

	mustProfileRequest(app, protocol.DesktopRenameMessage{Cmd: protocol.CmdDesktopRename, RequestID: "rename", DesktopID: slot5, Name: "Renamed"}, "rename")
	testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
		renamed := 0
		for _, m := range e.Members {
			if (m.ID == "alder" || m.ID == "trellis") && m.LaunchDesktop != nil && protocol.Deref(m.LaunchDesktop.Label) == "5 · Renamed" {
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
	if own == "" || protocol.Deref(defined.LaunchDesktop.Label) != "Nightly check (no ⌘ number)" {
		t.Fatalf("default launch desktop setting = %+v, want lazy own desktop label", defined.LaunchDesktop)
	}
	if _, exists := viewProfile(t, w, profile).desktops[own]; exists {
		t.Fatalf("creating an automation eagerly created an empty desktop %s", own)
	}
	run, err := cli.AutomationRun(defined.ID, "first", "")
	if err != nil {
		t.Fatal(err)
	}
	session := protocol.Deref(run.SessionID)
	w.Launched(string(session))
	after := viewProfile(t, w, profile)
	created, _ := after.paneOf(t, string(session))
	if created.Name != "Nightly check" || created.ShortcutSlot != nil {
		t.Fatalf("lazily created automation desktop = %+v, want unslotted Nightly check", created)
	}
	assertBackgroundPlacement(t, w, profile, string(session), created.ID, current, active)
	rebound := readLaunchSetting(app, "automation", fmt.Sprint(defined.ID))
	if protocol.Deref(rebound.Setting.DesktopID) != created.ID || protocol.Deref(rebound.Setting.Label) != "Nightly check (no ⌘ number)" {
		t.Fatalf("launch setting after first run = %+v, want rebound to %s", rebound, created.ID)
	}
}

func TestUpgradingAnInstallWithOnlyAutomationsGivesEachItsDesktopAndPlacesExistingAgents(t *testing.T) {
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
	placedPane(t, w, "existing")
	got := readLaunchSetting(app, "automation", "1")
	if !got.Confirmed || protocol.Deref(got.Setting.Label) != "Local check (no ⌘ number)" {
		t.Fatalf("the upgrade did not give the automation its desktop: %+v", got)
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
	if err := cli.RunCommand("terminal move", protocol.SessionID(session), map[string]any{"terminal": session, "desktop": target.ID}, new(protocol.TerminalMoveResult)); err != nil {
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
		left := viewProfile(t, w, profile).profile.CurrentDesktopID
		named := createDesktop(app, profile)
		mustProfileRequest(app, protocol.DesktopRenameMessage{Cmd: protocol.CmdDesktopRename, RequestID: "name", DesktopID: named.ID, Name: "Notes"}, "name")
		shown := createDesktop(app, profile)
		w.advance(time.Hour)
		view := viewProfile(t, w, profile)
		if _, exists := view.desktops[named.ID]; !exists {
			t.Fatal("an empty named desktop was removed")
		}
		_, leftRemains := view.desktops[left]
		if _, shownRemains := view.desktops[shown.ID]; leftRemains || !shownRemains || view.profile.CurrentDesktopID != shown.ID {
			t.Fatalf("after an hour the profile holds %v with %s current, want the empty desktop the user left removed and the empty current desktop %s kept", view.desktops, view.profile.CurrentDesktopID, shown.ID)
		}
		mustProfileRequest(app, protocol.DesktopRenameMessage{Cmd: protocol.CmdDesktopRename, RequestID: "unname", DesktopID: named.ID, Name: ""}, "unname")
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

func TestACompletedMigrationDoesNotReopenForALaterAutomation(t *testing.T) {
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
	finished := readLaunchSetting(w.App(), "automation", "1")
	if !finished.Confirmed || protocol.Deref(finished.Setting.DesktopID) == "" {
		t.Fatalf("a home waiting for placement opened without the automation on its own desktop: %+v", finished)
	}
	later := applyAutomation(t, w.Client(), definition)
	w.restart()
	for _, id := range []string{"1", fmt.Sprint(later.ID)} {
		if got := readLaunchSetting(w.App(), "automation", id); protocol.Deref(got.Setting.DesktopID) == "" {
			t.Fatalf("automation %s lost its desktop after a later automation and a restart: %+v", id, got)
		}
	}
	if got := readLaunchSetting(w.App(), "automation", "1"); protocol.Deref(got.Setting.DesktopID) != protocol.Deref(finished.Setting.DesktopID) {
		t.Fatalf("a restart moved the first automation from %s to %+v", protocol.Deref(finished.Setting.DesktopID), got.Setting)
	}
}

func TestAFreshInstanceStartsCrewFoundAtFirstStartOnTheirOwnDesktopsWithoutALaunchReview(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	profile := app.SelectedProfile()
	view := viewProfile(t, w, profile)
	if len(view.desktops) != 1 {
		t.Fatalf("fresh instance created %d desktops before any crew woke: %+v", len(view.desktops), view.desktops)
	}
	for member, name := range map[string]string{"alder": "Alder", "keel": "Keel", "trellis": "Trellis"} {
		item := readLaunchSetting(app, "crew", member)
		wantLabel := name + " (no ⌘ number)"
		if !item.Confirmed || protocol.Deref(item.Setting.DesktopID) == "" || protocol.Deref(item.Setting.Label) != wantLabel {
			t.Fatalf("%s starts on %+v, want confirmed lazy own desktop %q", member, item, wantLabel)
		}
	}
	giveImportedMemberAHarness(t, cli, "alder")
	wake := startCrew(t, cli, "alder", "")
	w.Launched(string(wake.SessionID))
	desktop, _ := viewProfile(t, w, profile).paneOf(t, string(wake.SessionID))
	if desktop.Name != "Alder" || desktop.ShortcutSlot != nil {
		t.Fatalf("woken alder placed on %+v, want unslotted Alder desktop", desktop)
	}
}

func TestEphemeralOwnLaunchDesktopsReapWhenEmptyAndRecreateOnNextRun(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		profile := app.SelectedProfile()
		homeDesktop := viewProfile(t, w, profile).profile.CurrentDesktopID
		w.bubbleClaude(t, app, "anchor")
		dir := w.Path("nightly")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		makeSpec := func(name string) string {
			return fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nname: %s\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", name, dir)
		}
		defined := applyAutomation(t, cli, makeSpec("Nightly check"))
		if len(viewProfile(t, w, profile).desktops) != 1 {
			t.Fatalf("defining an automation created an eager empty desktop: %+v", viewProfile(t, w, profile).desktops)
		}

		// User-kept launch desktops that must never be reaped when empty:
		// 1. Slotted launch desktop
		slottedDef := applyAutomation(t, cli, makeSpec("Slotted check"))
		slottedChoice := writeLaunchChoice(app, "automation", fmt.Sprint(slottedDef.ID), protocol.LaunchDesktopSetting{DesktopName: protocol.Ptr("Slotted check"), DesktopID: protocol.Ptr(profile + "/desktop_5")})
		slottedID := protocol.Deref(slottedChoice.Setting.DesktopID)
		// 2. Custom-named unslotted launch desktop (name differs from item's own name)
		customDef := applyAutomation(t, cli, makeSpec("Custom check"))
		customChoice := writeLaunchChoice(app, "automation", fmt.Sprint(customDef.ID), protocol.LaunchDesktopSetting{DesktopName: protocol.Ptr("Ops")})
		customID := protocol.Deref(customChoice.Setting.DesktopID)
		// 3. Shared unslotted launch desktop (two items share it)
		sharedDefA := applyAutomation(t, cli, makeSpec("Shared check"))
		sharedDefB := applyAutomation(t, cli, makeSpec("Shared buddy"))
		sharedChoice := writeLaunchChoice(app, "automation", fmt.Sprint(sharedDefA.ID), protocol.LaunchDesktopSetting{DesktopName: protocol.Ptr("Shared check")})
		sharedID := protocol.Deref(sharedChoice.Setting.DesktopID)
		writeLaunchChoice(app, "automation", fmt.Sprint(sharedDefB.ID), protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(sharedID)})

		// Run the default-own-desktop automation: lazily creates its unslotted desktop.
		firstRun, err := cli.AutomationRun(defined.ID, "first", "")
		if err != nil {
			t.Fatal(err)
		}
		firstSession := string(protocol.Deref(firstRun.SessionID))
		w.bootBubbleClaude(t, firstSession)
		firstDesktop, _ := viewProfile(t, w, profile).paneOf(t, firstSession)
		if firstDesktop.Name != "Nightly check" || firstDesktop.ShortcutSlot != nil || !protocol.Deref(firstDesktop.Ephemeral) {
			t.Fatalf("first run desktop = %+v", firstDesktop)
		}

		// Switch to it, close its run while viewing it: must stay alive while current.
		switchDesktop(app, profile, firstDesktop.ID)
		closeSession(t, cli, firstSession, "done")
		awaitClosed(app, firstSession)
		w.advance(time.Hour)
		if _, exists := viewProfile(t, w, profile).desktops[firstDesktop.ID]; !exists {
			t.Fatal("currently viewed empty ephemeral launch desktop was reaped")
		}

		// Leave it: stays for 29s grace, reaped at 30s.
		switchDesktop(app, profile, homeDesktop)
		w.advance(29 * time.Second)
		if _, exists := viewProfile(t, w, profile).desktops[firstDesktop.ID]; !exists {
			t.Fatal("ephemeral launch desktop was reaped before the 30s grace elapsed")
		}
		w.advance(time.Second)
		awaitDesktopRemoved(t, w, profile, firstDesktop.ID)

		// All user-kept launch desktops survived the entire hour+30s and are non-ephemeral.
		kept := viewProfile(t, w, profile).desktops
		for label, id := range map[string]string{"slotted": slottedID, "custom-named": customID, "shared": sharedID} {
			desktop, exists := kept[id]
			if !exists {
				t.Fatalf("%s launch desktop %s was unexpectedly reaped", label, id)
			}
			if protocol.Deref(desktop.Ephemeral) {
				t.Fatalf("%s launch desktop %s was unexpectedly marked ephemeral", label, id)
			}
		}

		// Launch setting still shows the automation's own desktop label, and a second run recreates it.
		setting := readLaunchSetting(app, "automation", fmt.Sprint(defined.ID))
		if protocol.Deref(setting.Setting.Label) != "Nightly check (no ⌘ number)" {
			t.Fatalf("launch setting after reap = %+v", setting)
		}
		secondRun, err := cli.AutomationRun(defined.ID, "second", "")
		if err != nil {
			t.Fatal(err)
		}
		secondSession := string(protocol.Deref(secondRun.SessionID))
		w.bootBubbleClaude(t, secondSession)
		secondDesktop, _ := viewProfile(t, w, profile).paneOf(t, secondSession)
		if secondDesktop.Name != "Nightly check" || secondDesktop.ShortcutSlot != nil || secondDesktop.ID == firstDesktop.ID || !protocol.Deref(secondDesktop.Ephemeral) {
			t.Fatalf("recreated automation desktop = %+v (previous %s)", secondDesktop, firstDesktop.ID)
		}

		// Renaming the automation while its ephemeral desktop is live keeps it ephemeral and updates its name.
		renamedSpec := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nid: %d\nname: Daily check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", defined.ID, dir)
		applyAutomation(t, cli, renamedSpec)
		if got := viewProfile(t, w, profile).desktops[secondDesktop.ID]; got.Name != "Daily check" || !protocol.Deref(got.Ephemeral) {
			t.Fatalf("renamed ephemeral desktop = %+v", got)
		}

		// Deleting the automation while its ephemeral desktop exists still lets it reap once empty.
		if err := cli.AutomationDelete(defined.ID); err != nil {
			t.Fatal(err)
		}
		closeSession(t, cli, secondSession, "done")
		awaitClosed(app, secondSession)
		w.advance(30 * time.Second)
		awaitDesktopRemoved(t, w, profile, secondDesktop.ID)

		// Crew member's own launch desktop follows the same lazy -> reap -> recreate lifecycle.
		agent := "claude"
		if _, err := cli.CrewSet("chief", &dir, &agent, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		chiefStart1, err := cli.CrewStart("chief", "", "")
		if err != nil {
			t.Fatal(err)
		}
		chiefSession1 := string(chiefStart1.SessionID)
		w.bootBubbleClaude(t, chiefSession1)
		chiefDesktop1, _ := viewProfile(t, w, profile).paneOf(t, chiefSession1)
		if chiefDesktop1.Name != "Chief" || chiefDesktop1.ShortcutSlot != nil || !protocol.Deref(chiefDesktop1.Ephemeral) {
			t.Fatalf("first chief desktop = %+v", chiefDesktop1)
		}
		if _, err := cli.CrewHandoff(chiefStart1.SessionID, "done", false); err != nil {
			t.Fatal(err)
		}
		awaitClosed(app, chiefSession1)
		w.advance(29 * time.Second)
		if _, exists := viewProfile(t, w, profile).desktops[chiefDesktop1.ID]; !exists {
			t.Fatal("chief ephemeral desktop was reaped before the 30s grace elapsed")
		}
		w.advance(time.Second)
		awaitDesktopRemoved(t, w, profile, chiefDesktop1.ID)

		chiefStart2, err := cli.CrewStart("chief", "", "")
		if err != nil {
			t.Fatal(err)
		}
		chiefSession2 := string(chiefStart2.SessionID)
		w.bootBubbleClaude(t, chiefSession2)
		chiefDesktop2, _ := viewProfile(t, w, profile).paneOf(t, chiefSession2)
		if chiefDesktop2.Name != "Chief" || chiefDesktop2.ShortcutSlot != nil || chiefDesktop2.ID == chiefDesktop1.ID || !protocol.Deref(chiefDesktop2.Ephemeral) {
			t.Fatalf("recreated chief desktop = %+v (previous %s)", chiefDesktop2, chiefDesktop1.ID)
		}
	})
}

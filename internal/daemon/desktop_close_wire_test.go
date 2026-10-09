package daemon_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func requestCloseDesktop(app *testworld.Peer, desktop protocol.Desktop) protocol.ProfileActionResultMessage {
	id := uuid.NewString()
	return profileRequest(app, protocol.DesktopCloseMessage{Cmd: protocol.CmdDesktopClose, RequestID: id, DesktopID: desktop.ID, ExpectedRevision: desktop.Revision}, id)
}

func TestCloseDesktopSelectsItsNeighbourAndRefusesTheLast(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profile := app.SelectedProfile()
		first := app.Initial.Desktops[0]
		second := createDesktop(app, profile)
		third := createDesktop(app, profile)
		id := uuid.NewString()
		mustProfileRequest(app, protocol.DesktopSetCurrentMessage{Cmd: protocol.CmdDesktopSetCurrent, RequestID: id, ProfileID: profile, DesktopID: second.ID}, id)
		result := requestCloseDesktop(app, second)
		if !result.Success || result.Profile.CurrentDesktopID != first.ID {
			t.Fatalf("close middle: %+v", result)
		}
		if _, exists := viewProfile(t, w, profile).desktops[second.ID]; exists {
			t.Fatal("closed desktop remains")
		}
		result = requestCloseDesktop(app, first)
		if !result.Success || result.Profile.CurrentDesktopID != third.ID {
			t.Fatalf("close first: %+v", result)
		}
		result = requestCloseDesktop(app, third)
		if result.Success || protocol.Deref(result.ErrorCode) != protocol.ProfileErrorCodeLastDesktop {
			t.Fatalf("close last: %+v", result)
		}
		w.restart()
		view := viewProfile(t, w, profile)
		if len(view.desktops) != 1 || view.profile.CurrentDesktopID != third.ID {
			t.Fatalf("after restart: %+v", view)
		}
	})
}

func TestCloseDesktopClosesAgentsAndShellsAndKeepsTheirLedger(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	profile := app.SelectedProfile()
	keeper := createDesktop(app, profile)
	switchDesktop(app, profile, app.Initial.Desktops[0].ID)
	agent := w.Spawn(app, fakeagent.Claude, w.Path("agent"))
	run := w.Launched(agent)
	app.TypeLine(agent, "keep this conversation")
	run.Prompted()
	run.Reply("Kept. <!-- attn:state=idle -->")
	shell := w.Spawn(app, shellHarness, w.Path("shell"))
	desktop, _ := viewProfile(t, w, profile).paneOf(t, agent)
	shellDesktop, _ := viewProfile(t, w, profile).paneOf(t, shell)
	if shellDesktop.ID != desktop.ID {
		t.Fatal("sessions must share the closing desktop")
	}
	docked := dockOnDesktop(app, desktop, protocol.DesktopDockTileMessage{TileID: "reference", TileKind: "browser", TileParams: protocol.Ptr("https://example.com"), Edge: protocol.LayoutDockEdgeRight})
	if !docked.Success {
		t.Fatal(protocol.Deref(docked.Error))
	}
	result := requestCloseDesktop(app, viewProfile(t, w, profile).desktops[desktop.ID])
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	if ids := queriedIDs(t, w.Client(), ""); len(ids) != 0 {
		t.Fatalf("still live: %v", ids)
	}
	closed := ledgerIDs(ledger(t, w.Client(), client.SessionListOptions{Closed: true}))
	if !slices.Contains(closed, agent) || !slices.Contains(closed, shell) {
		t.Fatalf("closed ledger: %v", closed)
	}
	view := viewProfile(t, w, profile)
	if len(view.desktops) != 1 || view.profile.CurrentDesktopID != keeper.ID {
		t.Fatalf("close arrangement: %+v", view)
	}
	reopened := reopenOverTheWebSocket(app, agent)
	if !reopened.Success {
		t.Fatalf("resume: %+v", reopened)
	}
	w.Launched(agent)
}

func TestCloseDesktopRefusesProtectedSessionsBeforeClosingAnything(t *testing.T) {
	for _, protection := range []string{"chief", "crew"} {
		t.Run(protection, func(t *testing.T) {
			w := newCrewWorld(t, fakeagent.Claude)
			app := w.App()
			profile := app.SelectedProfile()
			createDesktop(app, profile)
			ordinary := w.Spawn(app, fakeagent.Claude, w.Path("ordinary"))
			w.Launched(ordinary)
			protected := w.Spawn(app, fakeagent.Claude, w.Path("protected"))
			w.Launched(protected)
			desktop, _ := viewProfile(t, w, profile).paneOf(t, ordinary)
			if protection == "chief" {
				if made := setChiefOfStaff(app, protected, true); !made.Success {
					t.Fatal(protocol.Deref(made.Error))
				}
			} else {
				writeLaunchChoice(app, "crew", "alder", protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(desktop.ID)})
				wake := wakeCrew(t, w.Client(), "alder", "")
				protected = string(wake.SessionID)
				w.Launched(protected)
			}
			before := viewProfile(t, w, profile)
			result := requestCloseDesktop(app, before.desktops[desktop.ID])
			if result.Success || !strings.Contains(protocol.Deref(result.Error), protected) {
				t.Fatalf("protection refusal: %+v", result)
			}
			after := viewProfile(t, w, profile)
			if len(after.desktops) != len(before.desktops) || after.desktops[desktop.ID].TreeJson != before.desktops[desktop.ID].TreeJson || after.profile.CurrentDesktopID != before.profile.CurrentDesktopID {
				t.Fatal("refused close changed arrangement")
			}
			live := queriedIDs(t, w.Client(), "")
			if !slices.Contains(live, ordinary) || !slices.Contains(live, protected) {
				t.Fatalf("refused close ended a session: %v", live)
			}
		})
	}
}

func TestCrewWakeRecreatesItsClosedLaunchDesktop(t *testing.T) {
	for _, numbered := range []bool{false, true} {
		t.Run(fmt.Sprint(numbered), func(t *testing.T) {
			w := newCrewWorld(t, fakeagent.Claude)
			app := w.App()
			profile := app.SelectedProfile()
			setting := protocol.LaunchDesktopSetting{DesktopName: protocol.Ptr("Alder work")}
			if numbered {
				setting.DesktopID = protocol.Ptr(profile + "/desktop_7")
			}
			chosen := writeLaunchChoice(app, "crew", "alder", setting)
			old := protocol.Deref(chosen.Setting.DesktopID)
			if result := requestCloseDesktop(app, viewProfile(t, w, profile).desktops[old]); !result.Success {
				t.Fatal(protocol.Deref(result.Error))
			}
			w.restart()
			app = w.App()
			readLaunchSetting(app, "crew", "alder")
			member := crewRosterMember(t, w.Client(), "alder")
			request := uuid.NewString()
			saved := testworld.Request(app, protocol.CrewSetMessage{
				Cmd: protocol.CmdCrewSet, Member: "alder", RequestID: protocol.Ptr(request),
				ExpectedRevision: protocol.Ptr(member.Revision), Effort: protocol.Ptr("high"),
				LaunchDesktopSetting: &protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(old)},
			}, protocol.EventCrewSetResult, func(r protocol.CrewSetResultMessage) bool { return r.RequestID == request })
			if !saved.Success || saved.Member == nil || protocol.Deref(saved.Member.Effort) != "high" {
				t.Fatalf("edit crew while desktop closed: %s (%+v)", protocol.Deref(saved.Error), saved)
			}
			if _, exists := viewProfile(t, w, profile).desktops[old]; exists {
				t.Fatal("settings save recreated the closed launch desktop")
			}
			wake := wakeCrew(t, w.Client(), "alder", "")
			w.Launched(string(wake.SessionID))
			desktop, _ := viewProfile(t, w, profile).paneOf(t, string(wake.SessionID))
			if numbered && desktop.ID != old {
				t.Fatalf("numbered launch on %s, want %s", desktop.ID, old)
			}
			if !numbered && (desktop.ID == old || desktop.Name != "alder") {
				t.Fatalf("named launch: %+v", desktop)
			}
			rebound := readLaunchSetting(app, "crew", "alder")
			if protocol.Deref(rebound.Setting.DesktopID) != desktop.ID {
				t.Fatalf("launch binding: %+v", rebound)
			}
		})
	}
}

func TestAutomationRunRecreatesItsClosedLaunchDesktop(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	profile := app.SelectedProfile()
	dir := w.Path("check")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nname: Nightly check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", dir)
	createID := uuid.NewString()
	created := testworld.Request(app, protocol.AutomationApplyMessage{
		Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr(createID), DefinitionYaml: spec,
		LaunchDesktopSetting: &protocol.LaunchDesktopSetting{DesktopName: protocol.Ptr("Nightly check")},
	}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage](createID))
	if !created.Success || created.Definition == nil {
		t.Fatalf("create shared automation: %s", protocol.Deref(created.Error))
	}
	definition := *created.Definition
	old := protocol.Deref(definition.LaunchDesktop.DesktopID)
	writeLaunchChoice(app, "crew", "alder", protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(old)})
	if result := requestCloseDesktop(app, viewProfile(t, w, profile).desktops[old]); !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	w.restart()
	app = w.App()
	cli := w.Client()
	readLaunchSetting(app, "automation", fmt.Sprint(definition.ID))
	request := uuid.NewString()
	saved := testworld.Request(app, protocol.AutomationApplyMessage{
		Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr(request),
		DefinitionYaml: automationEditSpec(definition.ID, strings.Replace(spec, "Check locally.", "Edited while closed.", 1)),
		ExpectedID:     protocol.Ptr(definition.ID), ExpectedRevision: protocol.Ptr(definition.Revision),
		LaunchDesktopSetting: &protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(old)},
	}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage](request))
	if !saved.Success || saved.Definition == nil || saved.Definition.Revision != definition.Revision+1 {
		t.Fatalf("edit automation while desktop closed: %s (%+v)", protocol.Deref(saved.Error), saved)
	}
	if _, exists := viewProfile(t, w, profile).desktops[old]; exists {
		t.Fatal("settings save recreated the closed launch desktop")
	}
	crewBefore := crewRosterMember(t, cli, "alder")
	result, err := cli.AutomationRun(definition.ID, "after-close", "")
	if err != nil {
		t.Fatal(err)
	}
	session := protocol.Deref(result.Run.SessionID)
	w.Launched(string(session))
	desktop, _ := viewProfile(t, w, profile).paneOf(t, string(session))
	if desktop.ID == old || desktop.Name != "Nightly check" {
		t.Fatalf("automation desktop: %+v", desktop)
	}
	rebound := readLaunchSetting(app, "automation", fmt.Sprint(definition.ID))
	if protocol.Deref(rebound.Setting.DesktopID) != desktop.ID {
		t.Fatalf("automation binding: %+v", rebound)
	}
	staleID := uuid.NewString()
	stale := testworld.Request(app, protocol.AutomationApplyMessage{
		Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr(staleID),
		DefinitionYaml: automationEditSpec(definition.ID, strings.Replace(spec, "Check locally.", "Edited after launch.", 1)),
		ExpectedID:     protocol.Ptr(definition.ID), ExpectedRevision: protocol.Ptr(saved.Definition.Revision),
		LaunchDesktopSetting: &protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(old)},
	}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage](staleID))
	if stale.Success || protocol.Deref(stale.ErrorCode) != "revision_conflict" {
		t.Fatalf("editor opened before recreation: %s (%+v)", protocol.Deref(stale.Error), stale)
	}
	current, err := cli.AutomationDefinition(definition.ID)
	if err != nil || current.Definition.Revision != saved.Definition.Revision+1 {
		t.Fatalf("recreated target revision: %+v, %v", current, err)
	}
	freshID := uuid.NewString()
	fresh := testworld.Request(app, protocol.AutomationApplyMessage{
		Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr(freshID),
		DefinitionYaml: automationEditSpec(definition.ID, strings.Replace(spec, "Check locally.", "Edited after launch.", 1)),
		ExpectedID:     protocol.Ptr(definition.ID), ExpectedRevision: protocol.Ptr(current.Definition.Revision),
		LaunchDesktopSetting: &protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(desktop.ID)},
	}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage](freshID))
	if !fresh.Success {
		t.Fatalf("edit after reloading recreated target: %s", protocol.Deref(fresh.Error))
	}
	shared := readLaunchSetting(app, "crew", "alder")
	if protocol.Deref(shared.Setting.DesktopID) != desktop.ID {
		t.Fatalf("shared target was split: %+v", shared)
	}
	crewStaleID := uuid.NewString()
	crewStale := testworld.Request(app, protocol.CrewSetMessage{
		Cmd: protocol.CmdCrewSet, Member: "alder", RequestID: protocol.Ptr(crewStaleID),
		ExpectedRevision: protocol.Ptr(crewBefore.Revision), Effort: protocol.Ptr("high"),
		LaunchDesktopSetting: &protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(old)},
	}, protocol.EventCrewSetResult, func(r protocol.CrewSetResultMessage) bool { return r.RequestID == crewStaleID })
	if crewStale.Success || !crewStale.Conflict || crewStale.Member == nil || crewStale.Member.LaunchDesktop == nil {
		t.Fatalf("crew editor before recreation: %s (%+v)", protocol.Deref(crewStale.Error), crewStale)
	}
	if protocol.Deref(crewStale.Member.LaunchDesktop.DesktopID) != desktop.ID {
		t.Fatalf("crew conflict must return replacement: %+v", crewStale.Member)
	}
	crewFreshID := uuid.NewString()
	crewFresh := testworld.Request(app, protocol.CrewSetMessage{
		Cmd: protocol.CmdCrewSet, Member: "alder", RequestID: protocol.Ptr(crewFreshID),
		ExpectedRevision: protocol.Ptr(crewStale.Member.Revision), Effort: protocol.Ptr("high"),
		LaunchDesktopSetting: &protocol.LaunchDesktopSetting{DesktopID: protocol.Ptr(desktop.ID)},
	}, protocol.EventCrewSetResult, func(r protocol.CrewSetResultMessage) bool { return r.RequestID == crewFreshID })
	if !crewFresh.Success {
		t.Fatalf("crew edit after replacement: %s", protocol.Deref(crewFresh.Error))
	}
	wake := wakeCrew(t, cli, "alder", "")
	w.Launched(string(wake.SessionID))
	crewDesktop, _ := viewProfile(t, w, profile).paneOf(t, string(wake.SessionID))
	if crewDesktop.ID != desktop.ID || crewDesktop.Name != "Nightly check" {
		t.Fatalf("second launch must share the first item's replacement: %+v", crewDesktop)
	}

}

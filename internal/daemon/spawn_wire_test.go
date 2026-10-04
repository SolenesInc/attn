package daemon_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASpawnTheDaemonRefusesRegistersNothing(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	shell := fakeagent.Harness(protocol.AgentShellValue)
	for _, c := range []struct {
		name   string
		agent  fakeagent.Harness
		change func(*protocol.SpawnSessionMessage)
	}{
		{"an unknown agent", "no-such-agent", func(*protocol.SpawnSessionMessage) {}},
		{"a shell with an initial prompt", shell, func(m *protocol.SpawnSessionMessage) { m.InitialPrompt = protocol.Ptr("hello") }},
		{"zero columns", shell, func(m *protocol.SpawnSessionMessage) { m.Cols = 0 }},
		{"oversized dimensions", shell, func(m *protocol.SpawnSessionMessage) { m.Cols, m.Rows = 65536, 65536 }},
	} {
		refuseSpawnLikeTheApp(w, app, c.agent, w.Path(strings.ReplaceAll(c.name, " ", "-")), c.change)
	}

	for _, c := range []struct {
		name, profile, desktop, refusal string
	}{
		{"an unknown profile", "profile-missing", "", `profile "profile-missing" does not exist`},
	} {
		id := uuid.NewString()
		refused := testworld.Request(app, protocol.SpawnSessionMessage{
			Cmd: protocol.CmdSpawnSession, ID: id, Agent: protocol.AgentShellValue, Cwd: w.Path("shell"),
			ProfileID: c.profile, Placement: &protocol.SessionPlacement{DesktopID: protocol.Ptr(c.desktop)}, Cols: 100, Rows: 30,
		}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == id })
		if refused.Success || !strings.Contains(protocol.Deref(refused.Error), c.refusal) {
			t.Errorf("a spawn into %s = %+v, want it refused with %q", c.name, refused, c.refusal)
		}
	}

	if sessions := w.App().Initial.Sessions; len(sessions) != 0 {
		t.Errorf("the refused spawns left sessions %+v", sessions)
	}
}

func TestASpawnWithoutALabelIsNamedAfterItsDirectoryOnItsDesktop(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := filepath.Join(w.Path("projects"), "myproj")
	if paths := locationPaths(recentLocations(app, 50)); slices.Contains(paths, cwd) {
		t.Fatalf("recent locations %v list %s before anything ran there", paths, cwd)
	}
	spawned, desktop, pane := w.RequestSpawn(app, fakeagent.Claude, cwd)
	if !spawned.Success || desktop == "" || pane == "" {
		t.Fatalf("spawn = %+v, want it placed on the current desktop", spawned)
	}
	w.Launched(spawned.ID)

	registered := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.ID == spawned.ID
	})
	if registered.Session.Label != "myproj" || registered.Session.ProfileID != app.SelectedProfile() {
		t.Errorf("registered %q in profile %q, want myproj in %s", registered.Session.Label, registered.Session.ProfileID, app.SelectedProfile())
	}
	if paths := locationPaths(recentLocations(app, 50)); !slices.Contains(paths, cwd) {
		t.Errorf("recent locations %v do not list %s", paths, cwd)
	}
}

func TestTwoSpawnsOfOneSessionStartOneAgent(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Pi)
	app := w.App()
	awaitAgentAvailable(app, string(fakeagent.Pi))
	for _, agent := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Pi} {
		cwd := w.Path(string(agent))
		boot := w.HoldNextBoot()
		first, _, _ := w.RequestSpawn(app, agent, cwd)
		if !first.Success {
			t.Fatalf("spawn %s: %s", agent, protocol.Deref(first.Error))
		}
		again := testworld.Request(app, protocol.SpawnSessionMessage{
			Cmd: protocol.CmdSpawnSession, ID: first.ID, Agent: string(agent), Cwd: cwd, ProfileID: app.SelectedProfile(), Cols: 100, Rows: 30,
		}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == first.ID })
		if !again.Success {
			t.Errorf("spawning the live %s session again: %s", agent, protocol.Deref(again.Error))
		}
		boot()
		run := w.Launched(first.ID)
		app.TypeLine(first.ID, "fix the build")
		if got := run.Prompted(); got != "fix the build" {
			t.Errorf("the %s launched first received %q, want the user's prompt", agent, got)
		}
		testworld.AwaitSession(app, first.ID, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		run.Reply("Fixed. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, first.ID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		registrations := 0
		for _, e := range app.Received() {
			if e.Event == protocol.EventSessionRegistered && e.Session != nil && e.Session.ID == first.ID {
				registrations++
			}
		}
		if registrations != 1 {
			t.Errorf("the %s session was announced as registered %d times, want once", agent, registrations)
		}

		app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: first.ID})
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == first.ID })
		w.Spawn(app, agent, cwd, func(m *protocol.SpawnSessionMessage) {
			m.ID = first.ID
			if agent == fakeagent.Claude {
				m.ResumeSessionID = protocol.Ptr(first.ID)
			}
		})
		next := w.Launched(first.ID)
		if agent == fakeagent.Claude && (!next.Resumed || next.ConversationID != run.ConversationID) {
			t.Errorf("the next claude launch ran %q, want the respawn resuming conversation %s", next.Argv, run.ConversationID)
		}
		app.TypeLine(first.ID, "run the tests")
		if got := next.Prompted(); got != "run the tests" {
			t.Errorf("the respawned %s received %q, want the user's prompt", agent, got)
		}
	}
}

func TestAChiefStartsOnTheConfiguredChiefModelAndEffort(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "chief_model_claude", "claude-opus-4")
	setSetting(t, app, "chief_effort_claude", "high")
	chief := w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) }))
	model, _ := flagValue(chief.Argv, "--model")
	effort, _ := flagValue(chief.Argv, "--effort")
	if model != "claude-opus-4" || effort != "high" {
		t.Errorf("the chief ran claude %q, want the configured chief model and effort", chief.Argv)
	}
}

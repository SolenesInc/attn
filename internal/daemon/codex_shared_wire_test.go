package daemon_test

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASharedCodexLaunchIsOneSessionThatWorksWaitsAndCountsItsUsage(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			setSetting(t, app, "codex_shared_enabled", "true")
			if direct {
				setSetting(t, app, "direct_agent_launch_enabled", "true")
			}
			session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
			codex := w.Launched(session)
			if !slices.Contains(codex.Argv, "--remote") {
				t.Fatalf("the shared launch ran %q, want Codex connected to an app-server", codex.Argv)
			}

			app.TypeLine(session, "find the flaky checkout test")
			if got := codex.Prompted(); got != "find the flaky checkout test" {
				t.Fatalf("codex received %q", got)
			}
			testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			question := "It races the tax lookup. Add a lock? <!-- attn:state=waiting_input -->"
			codex.Reply(question)
			testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
			if usage := awaitUsageTokens(app, session, claudeTokens(question)); usage.MeasurementIncomplete != nil {
				t.Errorf("usage = %+v, want it complete", usage)
			}

			var codexSessions []string
			for _, s := range w.App().Initial.Sessions {
				if s.Agent == protocol.SessionAgentCodex {
					codexSessions = append(codexSessions, string(s.ID))
				}
			}
			if !slices.Equal(codexSessions, []string{session}) {
				t.Errorf("Codex sessions = %v, want only %s", codexSessions, session)
			}
			if servers := w.CodexServers(); len(servers) != 1 {
				t.Errorf("app-servers launched = %v, want one", servers)
			}
		})
	}
}

func TestWithSharedCodexOffCodexRunsAloneInItsTerminal(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("Found it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if slices.Contains(codex.Argv, "--remote") {
		t.Errorf("Codex ran %q, want it on its own", codex.Argv)
	}
	if servers := w.CodexServers(); len(servers) != 0 {
		t.Errorf("app-servers launched = %v, want none", servers)
	}
}

func TestASharedCodexAppServerThatExitsStartsAgainAndTheTerminalCarriesOn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	conversation := codex.ConversationID

	codex.CrashAppServer()
	app.TypeLine(session, "add a discount field")
	if got := codex.Prompted(); got != "add a discount field" {
		t.Fatalf("codex received %q after its app-server exited", got)
	}
	if codex.ConversationID != conversation {
		t.Fatalf("the terminal shows conversation %s, want %s", codex.ConversationID, conversation)
	}
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if servers := w.CodexServers(); len(servers) != 2 {
		t.Errorf("app-servers launched = %v, want the first and the one that replaced it", servers)
	}
}

func TestASharedCodexLaunchThatCannotBePlacedLetsItsAppServerStop(t *testing.T) {
	w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	notes, desktop, anchor := w.RequestSpawn(app, fakeagent.Claude, w.Path("notes"))
	if !notes.Success {
		t.Fatalf("spawn the session beside which Codex launches: %s", protocol.Deref(notes.Error))
	}
	w.Launched(string(notes.ID))
	cwd := w.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	shop := protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(uuid.NewString()), Agent: string(fakeagent.Codex),
		Cwd: cwd, ProfileID: app.SelectedProfile(), Cols: 100, Rows: 30,
		Placement: &protocol.SessionPlacement{DesktopID: protocol.Ptr(desktop), AnchorPaneID: protocol.Ptr(anchor)},
	}

	boot := w.HoldNextBoot()
	app.Send(shop)
	w.AwaitHeldBoot()
	if closed := closeTileFromApp(w.ConnectApp(), desktop, anchor); !closed.Success {
		t.Fatalf("close the tile Codex launches beside: %s", protocol.Deref(closed.Error))
	}
	boot()
	if placed := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == shop.ID }); placed.Success {
		t.Fatalf("a shared Codex launch beside a tile that closed under it was placed")
	}
	w.CodexServer().AwaitStopped()
}

func TestASharedCodexCrewMemberWithAwarenessDirsWakes(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Codex)
	app := w.App()
	cli := w.Client()
	setSetting(t, app, "codex_shared_enabled", "true")
	workDir, notes := w.Path("keel-work"), w.Path("keel-notes")
	for _, dir := range []string{workDir, notes} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setCrew(t, cli, "keel", protocol.CrewSetMessage{Cwd: protocol.Ptr(workDir), Agent: protocol.Ptr("codex"), AwarenessDirs: []string{notes}})
	keel := string(wakeCrew(t, cli, "keel", "").SessionID)
	codex := w.Launched(keel)
	if !slices.Contains(codex.Argv, "--remote") {
		t.Fatalf("keel ran %q, want Codex connected to an app-server", codex.Argv)
	}
	if got := codex.Prompted(); !strings.Contains(got, "You have been woken") {
		t.Fatalf("keel's codex received %q, want its wake prompt", got)
	}
}

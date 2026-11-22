package main_test

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const buildBeforeHarnessCommands = "59d967b97f505b2132cdd96c3652249eff7ebe48"

func TestATerminalThePreviousDaemonReloadsDuringAnUpdateRunsOnTheInstalledWorkerAsBefore(t *testing.T) {
	t.Parallel()
	previous, current := testworld.AttnBinaryFromCommit(t, buildBeforeHarnessCommands), testworld.AttnBinary(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex), testworld.OnPTYBackend(testworld.WorkerPTY))
	s.InstallBuild(previous, "")
	s.StartBinary(filepath.Join(s.Dir, "install", "attnd"))
	app := s.AppOfAnotherBuild()
	turn := func(session string, run *fakeagent.Run, state protocol.SessionState) {
		t.Helper()
		app.TypeLine(session, "add a note to checkout")
		run.Prompted()
		run.Reply("Done. <!-- attn:state=" + string(state) + " -->")
		testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == state })
	}
	reload := func(session string) *fakeagent.Run {
		t.Helper()
		result := testworld.Request(app, protocol.ReloadSessionMessage{
			Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30,
		}, protocol.EventReloadSessionResult, func(event protocol.ReloadSessionResultMessage) bool {
			return string(event.ID) == session
		})
		if !result.Success {
			t.Fatalf("the previous daemon could not reload %s onto the installed worker: %s", session, protocol.Deref(result.Error))
		}
		return s.Launched(session)
	}
	busyTitles := func(session string) int {
		t.Helper()
		var explained protocol.StateExplainResult
		s.Attn("state", "explain", session, "--json").JSON(t, &explained)
		busy := 0
		for _, obs := range explained.Observations {
			if obs.Source == "heartbeat" && obs.Claim == "busy" {
				busy++
			}
		}
		return busy
	}
	claude := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	first := s.Launched(claude)
	turn(claude, first, protocol.SessionStateIdle)
	codex := s.Spawn(app, fakeagent.Codex, s.Path("cart"))
	turn(codex, s.Launched(codex), protocol.SessionStateIdle)

	s.InstallBuild(current, "")

	resumed := reload(claude)
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("the reload resumed=%t conversation %q, want %q", resumed.Resumed, resumed.ConversationID, first.ConversationID)
	}
	titled := reload(codex)
	before := busyTitles(codex)
	turn(codex, titled, protocol.SessionStateWaitingInput)
	if busyTitles(codex) <= before {
		t.Fatalf("the installed worker read no busy title in a turn on the Codex terminal the previous daemon reloaded")
	}
}

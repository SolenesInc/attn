package daemon_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestRecoveryKeepsEveryConversationThatCanResumeWithItsPaneAndReapsTheRest(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "auto_approve_enabled", "false")
	cwd := w.Path("shop")
	begin := func(agent fakeagent.Harness) (string, *fakeagent.Run) {
		t.Helper()
		session := w.Spawn(app, agent, cwd)
		run := w.Launched(session)
		app.TypeLine(session, "add a discount field to checkout")
		run.Prompted()
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		return session, run
	}
	settleOn := func(session string, run *fakeagent.Run, state protocol.SessionState) {
		t.Helper()
		run.Reply("Done here. <!-- attn:state=" + string(state) + " -->")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool {
			return s.State == state && protocol.Deref(s.StateReason) == "classifier_verdict"
		})
	}

	claudeWorking, _ := begin(fakeagent.Claude)
	claudeAsking, _ := begin(fakeagent.Claude)
	if err := cli.RecordNotification(protocol.TerminalID(w.Terminal(claudeAsking)), "permission_prompt", "Allow edit?"); err != nil {
		t.Fatalf("ask for approval: %v", err)
	}
	testworld.AwaitSession(app, claudeAsking, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	codexIdle, codexIdleRun := begin(fakeagent.Codex)
	settleOn(codexIdle, codexIdleRun, protocol.SessionStateIdle)
	claudeWaiting, claudeWaitingRun := begin(fakeagent.Claude)
	settleOn(claudeWaiting, claudeWaitingRun, protocol.SessionStateWaitingInput)
	shell := w.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), cwd)
	lost, lostRun := begin(fakeagent.Claude)
	settleOn(lost, lostRun, protocol.SessionStateIdle)
	sessionRecoveryDeleteTranscript(t, lostRun.ConversationID)
	untouched := w.Spawn(app, fakeagent.Claude, cwd)
	w.Launched(untouched)

	w.restartHoldingRevives()
	sessionRecoveryExpect(t, w.App().Initial,
		[]string{claudeWorking, claudeAsking, codexIdle, claudeWaiting, shell},
		[]string{lost, untouched})

	sessionRecoveryDeleteTranscript(t, codexIdleRun.ConversationID)
	w.restartHoldingRevives()
	sessionRecoveryExpect(t, w.App().Initial,
		[]string{claudeWorking, claudeAsking, claudeWaiting, shell},
		[]string{codexIdle, lost, untouched})
}

func TestARestartBringsBackEveryAgentItLostAndLeavesShellsToBeOpened(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app := w.App()
	cwd := w.Path("shop")
	converse := func(agent fakeagent.Harness) (string, *fakeagent.Run) {
		t.Helper()
		session := w.Spawn(app, agent, cwd)
		run := w.Launched(session)
		app.TypeLine(session, "add a discount field to checkout")
		run.Prompted()
		run.Reply("Done here. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		return session, run
	}
	claude, claudeRun := converse(fakeagent.Claude)
	codex, codexRun := converse(fakeagent.Codex)
	shell := w.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), cwd)

	w.restart()
	app = w.App()
	if opened := attachRevive(app, claude, 100, 30); !opened.Success {
		t.Errorf("opening an agent while it comes back answered %+v", opened)
	}
	for session, before := range map[string]*fakeagent.Run{claude: claudeRun, codex: codexRun} {
		if after := w.Launched(session); !after.Resumed || after.ConversationID != before.ConversationID {
			t.Errorf("%s came back as %q, want it resuming conversation %s", session, after.Argv, before.ConversationID)
		}
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	}
	if state := queriedSession(t, w.Client(), shell).State; state != protocol.SessionStateRecoverable {
		t.Errorf("the shell is %s, want it recoverable until it is opened", state)
	}
}

func sessionRecoveryExpect(t *testing.T, initial protocol.InitialStateMessage, recoverable, reaped []string) {
	t.Helper()
	sessions := map[string]protocol.Session{}
	for _, s := range initial.Sessions {
		sessions[string(s.ID)] = s
	}
	panes := map[string]bool{}
	for _, desktop := range initial.Desktops {
		for _, pane := range desktop.Panes {
			panes[string(pane.SessionID)] = true
		}
	}
	for _, id := range recoverable {
		s, ok := sessions[id]
		if !ok || s.State != protocol.SessionStateRecoverable || s.StateReason != nil || !panes[id] {
			t.Errorf("session %s came back present=%v %s (reason %q) with a pane=%v, want recoverable, unexplained, in its pane",
				id, ok, s.State, protocol.Deref(s.StateReason), panes[id])
		}
	}
	for _, id := range reaped {
		if _, ok := sessions[id]; ok || panes[id] {
			t.Errorf("session %s with nothing to resume survived the restart (session=%v pane=%v)", id, ok, panes[id])
		}
	}
	if !slices.ContainsFunc(initial.Warnings, func(w protocol.DaemonWarning) bool { return w.Code == "stale_sessions_pruned" }) {
		t.Errorf("warnings = %+v, want stale_sessions_pruned", initial.Warnings)
	}
}

func sessionRecoveryDeleteTranscript(t *testing.T, conversationID string) {
	t.Helper()
	removed := 0
	err := filepath.WalkDir(os.Getenv("ATTN_TOOL_HOME"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.Contains(entry.Name(), conversationID) {
			return err
		}
		removed++
		return os.Remove(path)
	})
	if err != nil || removed == 0 {
		t.Fatalf("delete the transcript of conversation %s: removed %d files (%v)", conversationID, removed, err)
	}
}

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

	w.restart()
	sessionRecoveryExpect(t, w.App().Initial,
		[]string{claudeWorking, claudeAsking, codexIdle, claudeWaiting, shell},
		[]string{lost, untouched})

	sessionRecoveryDeleteTranscript(t, codexIdleRun.ConversationID)
	w.restart()
	sessionRecoveryExpect(t, w.App().Initial,
		[]string{claudeWorking, claudeAsking, claudeWaiting, shell},
		[]string{codexIdle, lost, untouched})
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

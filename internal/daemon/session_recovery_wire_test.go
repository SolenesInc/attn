package daemon_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
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
	if err := cli.RecordNotification(claudeAsking, "permission_prompt", "Allow edit?"); err != nil {
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
	hooked := sessionRecoveryRegisterHookedConversation(t, cli, cwd)

	w.restart()
	sessionRecoveryExpect(t, w.App().Initial,
		[]string{claudeWorking, claudeAsking, codexIdle, claudeWaiting, shell},
		[]string{lost, untouched, hooked})

	sessionRecoveryDeleteTranscript(t, codexIdleRun.ConversationID)
	w.restart()
	sessionRecoveryExpect(t, w.App().Initial,
		[]string{claudeWorking, claudeAsking, claudeWaiting, shell},
		[]string{codexIdle, lost, untouched, hooked})
}

func sessionRecoveryExpect(t *testing.T, initial protocol.InitialStateMessage, recoverable, reaped []string) {
	t.Helper()
	sessions := map[string]protocol.Session{}
	for _, s := range initial.Sessions {
		sessions[s.ID] = s
	}
	panes := map[string]bool{}
	for _, workspace := range initial.Workspaces {
		if workspace.Layout == nil {
			continue
		}
		for _, pane := range workspace.Layout.Panes {
			panes[protocol.Deref(pane.SessionID)] = true
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

func sessionRecoveryRegisterHookedConversation(t *testing.T, cli *client.Client, cwd string) string {
	t.Helper()
	const id, nativeID = "hooked", "hooked-native-conversation"
	projects := filepath.Join(os.Getenv("ATTN_TOOL_HOME"), ".claude", "projects", "hooked")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	transcriptPath := filepath.Join(projects, nativeID+".jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"user","message":{"role":"user","content":"add a discount field to checkout"}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if err := cli.RegisterWithAgent(id, id, cwd, string(protocol.SessionAgentClaude)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := cli.ObserveAgentConversation(id, nativeID, transcriptPath); err != nil {
		t.Fatalf("bind conversation: %v", err)
	}
	if bound, err := cli.SessionTranscript(id, ""); err != nil || len(bound.Events) == 0 {
		t.Fatalf("the hooks bound no readable conversation: %+v (%v)", bound, err)
	}
	return id
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

package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestWhatTheUserTypesWhileAttnRingsTheDoorbellStaysInTheirDraft(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	gap := s.PauseAt(pausepoint.SessionInputPasteGap)
	waiting := s.PauseAt(pausepoint.SessionInputLaneContended)
	s.Start()
	app := s.App()
	const reviewer = "rev-1111-2222"
	register(t, s, reviewer, "reviewer")
	recipient := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	claude := s.Launched(recipient)
	converse(app, claude, recipient, "wait for the reviewer", "Waiting.")

	sent := s.Launch(testworld.Invocation{Args: []string{"agent", "msg", recipient, "the build is green"}, Session: reviewer})
	gap.Await()
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(recipient)), Data: "half a thought"})
	waiting.Await()
	waiting.Release()
	gap.Release()

	if got := claude.Prompted(); strings.Contains(got, "half a thought") || !strings.Contains(got, "attn agent inbox") {
		t.Errorf("the agent was prompted with %q, want attn's doorbell without the user's draft", got)
	}
	if result := sent.Wait(); result.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s", result.Code, result.Stderr)
	}
	app.AwaitScreen(recipient, "half a thought")
}

func TestAnApprovalPromptThatAppearsAfterAttnPastesItsMessageIsLeftForTheUser(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	gap := s.PauseAt(pausepoint.SessionInputPasteGap)
	s.Start()
	app := s.App()
	setSetting(t, app, "auto_approve_enabled", "true")
	const reviewer = "rev-1111-2222"
	register(t, s, reviewer, "reviewer")
	recipient := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(recipient)
	converse(app, codex, recipient, "wait for the reviewer", "Waiting.")

	sent := s.Launch(testworld.Invocation{Args: []string{"agent", "msg", recipient, "the build is green"}, Session: reviewer})
	gap.Await()
	app.AwaitScreen(recipient, "attn agent inbox")
	codex.AskApproval()
	app.AwaitScreen(recipient, "Yes, proceed")
	gap.Release()
	if result := sent.Wait(); result.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s", result.Code, result.Stderr)
	}

	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(recipient)), Data: "1"})
	if answer := codex.Answered(); answer != "1" {
		t.Fatalf("codex's approval prompt was answered with %q, want the user's %q", answer, "1")
	}
	if got := codex.Prompted(); !strings.Contains(got, "attn agent inbox") {
		t.Errorf("once the approval was answered codex was prompted with %q, want attn's doorbell", got)
	}
}

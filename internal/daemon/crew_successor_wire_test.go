package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestASuccessorKeepsItsDaysApprovalModeAndReturnsToTheMembersSavedPins(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "default_model_claude", "claude-opus-4-1")
	setSetting(t, app, "default_model_codex", "gpt-5.6-sol")
	setCrew(t, cli, "keel", protocol.CrewSetMessage{Agent: protocol.Ptr("claude"), Effort: protocol.Ptr("high")})
	setSetting(t, app, "auto_approve_enabled", "true")

	day := wakeCrew(t, cli, "keel", "codex")
	oneDay := w.Launched(day.SessionID)
	if model := crewLaunchFlag(oneDay.Argv, "--model"); oneDay.Harness != fakeagent.Codex || model != "gpt-5.6-sol" {
		t.Fatalf("a one-day codex wake launched %s on model %q, want codex on its own default gpt-5.6-sol", oneDay.Harness, model)
	}
	if !slices.Contains(oneDay.Argv, `approvals_reviewer="auto_review"`) {
		t.Fatal("the day launched without the reviewer approval mode the user had on")
	}

	setSetting(t, app, "auto_approve_enabled", "false")
	handed := crewHandoff(t, cli, day.SessionID, "Back to the usual harness tomorrow.", false, protocol.CrewDayCloseNap)
	next := w.Launched(protocol.Deref(handed.SessionID))
	model, effort := crewLaunchFlag(next.Argv, "--model"), crewLaunchFlag(next.Argv, "--effort")
	if next.Harness != fakeagent.Claude || model != "claude-opus-4-1" || effort != "high" {
		t.Errorf("the successor launched %s on model %q effort %q, want keel's saved claude on claude-opus-4-1 with effort high", next.Harness, model, effort)
	}
	if mode := crewLaunchFlag(next.Argv, "--permission-mode"); mode != "auto" {
		t.Errorf("the successor launched with permission mode %q, want the day's auto", mode)
	}
}

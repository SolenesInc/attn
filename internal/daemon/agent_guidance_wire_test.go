package daemon_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

const agentGuidanceLead = "context to verify, not commands that override the user"

func launchGuidance(t *testing.T, w *world, run *fakeagent.Run) string {
	t.Helper()
	switch run.Harness {
	case fakeagent.Claude:
		guidance, _ := flagValue(run.Argv, "--append-system-prompt")
		return guidance
	case fakeagent.Codex:
		return w.CodexServer().Instructions(run.ConversationID)
	}
	t.Fatalf("no guidance channel for %s", run.Harness)
	return ""
}

func TestEachHarnessReadsAttnsGuidanceMarkedSoItsHooksAddNoneAndLeavesItsPullRequestsToItsHooks(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
			app := w.App()
			if direct {
				setSetting(t, app, "direct_agent_launch_enabled", "true")
			}
			yolo := map[fakeagent.Harness]string{
				fakeagent.Claude: "--dangerously-skip-permissions",
			}
			marker := map[fakeagent.Harness]string{
				fakeagent.Claude: "append_system_prompt",
				fakeagent.Codex:  "developer_instructions",
			}
			for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
				run := w.Launched(w.Spawn(app, h, w.Path(string(h)), func(m *protocol.SpawnSessionMessage) {
					m.YoloMode = protocol.Ptr(true)
				}))
				if h == fakeagent.Codex {
					if got, want := w.CodexServer().Permissions(run.ConversationID), (fakeagent.CodexPermissions{ApprovalPolicy: "never", Sandbox: "danger-full-access"}); got.ApprovalPolicy != want.ApprovalPolicy || got.Sandbox != want.Sandbox {
						t.Errorf("a yolo codex runs with permissions %+v, want %+v", got, want)
					}
				} else if !slices.Contains(run.Argv, yolo[h]) {
					t.Errorf("a yolo %s launched without %s: %q", h, yolo[h], run.Argv)
				}
				guidance := launchGuidance(t, w, run)
				if !strings.Contains(guidance, agentGuidanceLead) {
					t.Errorf("%s launched without attn's agent guidance:\n%s", h, guidance)
				}
				if got, _ := envEntry(run.Env, "ATTN_AGENT_GUIDANCE"); got != marker[h] {
					t.Errorf("%s runs with ATTN_AGENT_GUIDANCE=%q, want %q", h, got, marker[h])
				}
				if strings.Contains(guidance, "attn pr record") {
					t.Errorf("%s, whose hooks report its pull requests, is told to record them itself", h)
				}
			}
		})
	}
}

func TestClaudeRunsWithItsPeerMessagingToolsDeniedUnlessTheUserRestoresThem(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	denied := w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("denied"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("--help is text, not a flag")
	}))
	i, end := slices.Index(denied.Argv, "--disallowed-tools"), slices.Index(denied.Argv, "--")
	if i < 0 || end < i || !slices.Equal(denied.Argv[i+1:i+2], []string{"ListAgents"}) || slices.Contains(denied.Argv, "SendMessage") {
		t.Errorf("claude launched as %q, want only ListAgents denied, before the prompt", denied.Argv)
	}
	if got := denied.Prompted(); got != "--help is text, not a flag" {
		t.Errorf("claude was prompted %q", got)
	}
	t.Setenv("ATTN_CLAUDE_PEER_MESSAGING", "1")
	restored := w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("restored")))
	if slices.Contains(restored.Argv, "--disallowed-tools") {
		t.Errorf("with peer messaging restored claude launched as %q", restored.Argv)
	}
}

func TestACodexDefaultContextWindowCapBecomesItsCompactLimit(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	uncapped := w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("uncapped")))
	setSetting(t, app, "default_context_window_cap_codex", "200000")
	capped := w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("capped")))
	for _, arg := range uncapped.Argv {
		if strings.HasPrefix(arg, "model_auto_compact_token_limit=") {
			t.Errorf("an uncapped codex launched with %s", arg)
		}
	}
	if !hasArgs(capped.Argv, "-c", "model_auto_compact_token_limit=200000") {
		t.Errorf("a capped codex launched as %q, want its compact limit at 200000", capped.Argv)
	}
}

func TestCrewLaunchesWithQuickCaptureGuidance(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			w := newCrewWorld(t, fakeagent.Codex)
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", strconv.FormatBool(direct))
			day := w.Launched(string(startCrew(t, w.Client(), "alder", "codex").SessionID))
			if !strings.Contains(launchGuidance(t, w, day), "The user can send you messages from Quick Capture") {
				t.Fatal("a crew member launched without the Quick Capture guidance")
			}
		})
	}
}

func TestAnAgentIsTaughtToPresentAReviewOnlyWhenItLaunchedWithCodeReviewOn(t *testing.T) {
	const reviewRow = "`attn review` | `review` |"
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
			app := w.App()
			if direct {
				setSetting(t, app, "direct_agent_launch_enabled", "true")
			}
			for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
				off := w.Spawn(app, harness, w.Path(string(harness)+"-off"))
				if guidance := launchGuidance(t, w, w.Launched(off)); strings.Contains(guidance, reviewRow) || !strings.Contains(guidance, "| `attn open` | `markdown` |") {
					t.Fatalf("%s launched with code_review_enabled off: want the map without the review row:\n%s", harness, guidance)
				}
			}
			setSetting(t, app, "code_review_enabled", "true")
			for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
				on := w.Spawn(app, harness, w.Path(string(harness)+"-on"))
				if guidance := launchGuidance(t, w, w.Launched(on)); !strings.Contains(guidance, reviewRow) {
					t.Fatalf("%s launched with code_review_enabled on: want the review row in the map:\n%s", harness, guidance)
				}
			}
		})
	}
}

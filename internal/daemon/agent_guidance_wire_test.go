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

func launchGuidance(t *testing.T, run *fakeagent.Run) string {
	t.Helper()
	switch run.Harness {
	case fakeagent.Claude:
		guidance, _ := flagValue(run.Argv, "--append-system-prompt")
		return guidance
	case fakeagent.Codex:
		var overrides []string
		for i, arg := range run.Argv {
			if arg == "-c" && i+1 < len(run.Argv) && strings.HasPrefix(run.Argv[i+1], "developer_instructions=") {
				overrides = append(overrides, run.Argv[i+1])
			}
		}
		if len(overrides) != 1 {
			t.Fatalf("codex launched with %d developer instructions: %q", len(overrides), run.Argv)
		}
		guidance, err := strconv.Unquote(strings.TrimPrefix(overrides[0], "developer_instructions="))
		if err != nil {
			t.Fatalf("codex developer instructions are not one quoted string: %v", err)
		}
		return guidance
	}
	t.Fatalf("no guidance channel for %s", run.Harness)
	return ""
}

func TestEachHarnessReadsAttnsGuidanceAndLeavesItsPullRequestsToItsHooks(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
			app := w.App()
			if direct {
				setSetting(t, app, "direct_agent_launch_enabled", "true")
			}
			yolo := map[fakeagent.Harness]string{
				fakeagent.Claude: "--dangerously-skip-permissions",
				fakeagent.Codex:  "--dangerously-bypass-approvals-and-sandbox",
			}
			for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
				run := w.Launched(w.Spawn(app, h, w.Path(string(h)), func(m *protocol.SpawnSessionMessage) {
					m.YoloMode = protocol.Ptr(true)
				}))
				if !slices.Contains(run.Argv, yolo[h]) {
					t.Errorf("a yolo %s launched without %s: %q", h, yolo[h], run.Argv)
				}
				guidance := launchGuidance(t, run)
				if !strings.Contains(guidance, agentGuidanceLead) {
					t.Errorf("%s launched without attn's agent guidance:\n%s", h, guidance)
				}
				if strings.Contains(guidance, "attn pr record") {
					t.Errorf("%s, whose hooks report its pull requests, is told to record them itself", h)
				}
			}
		})
	}
}

func TestAChiefIsGuidedAsTheChiefAndMarkedSoItsHooksAddNoAgentGuidance(t *testing.T) {
	for h, marker := range map[fakeagent.Harness]string{fakeagent.Claude: "append_system_prompt", fakeagent.Codex: "developer_instructions"} {
		t.Run(string(h), func(t *testing.T) {
			w := newWorld(t, h)
			app := w.App()
			chief := w.Launched(configureChiefOn(t, w, app, h, chiefModel(h)))
			worker := w.Launched(w.Spawn(app, h, w.Path("worker")))
			for _, tc := range []struct {
				name     string
				run      *fakeagent.Run
				guidance string
				marker   string
				unmarked string
			}{
				{"the chief", chief, "You are the Chief", "ATTN_CHIEF_GUIDANCE", "ATTN_AGENT_GUIDANCE"},
				{"a worker", worker, agentGuidanceLead, "ATTN_AGENT_GUIDANCE", "ATTN_CHIEF_GUIDANCE"},
			} {
				guidance := launchGuidance(t, tc.run)
				other := agentGuidanceLead
				if tc.guidance == agentGuidanceLead {
					other = "You are the Chief"
				}
				if !strings.Contains(guidance, tc.guidance) || strings.Contains(guidance, other) {
					t.Errorf("%s %s launched without only its own guidance:\n%s", tc.name, h, guidance)
				}
				if got, _ := envEntry(tc.run.Env, tc.marker); got != marker {
					t.Errorf("%s %s runs with %s=%q, want %q", tc.name, h, tc.marker, got, marker)
				}
				if got, ok := envEntry(tc.run.Env, tc.unmarked); ok {
					t.Errorf("%s %s also carries %s=%s", tc.name, h, tc.unmarked, got)
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

func TestQuickCaptureGuidanceFollowsTheExperimentAtEachLaunch(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", strconv.FormatBool(direct))
			for i, enabled := range []bool{false, true, false} {
				if i > 0 {
					setSetting(t, app, "quick_capture_enabled", strconv.FormatBool(enabled))
				}
				for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
					var id string
					if i == 0 && h == fakeagent.Claude {
						id = configureChiefOn(t, w, app, h, chiefModel(h))
					} else {
						id = string(wakeCrew(t, w.Client(), "chief", string(h)).SessionID)
					}
					chief := w.Launched(id)
					if got := strings.Contains(launchGuidance(t, chief), "The user can send you messages from Quick Capture"); got != enabled {
						t.Fatalf("%s chief Quick Capture guidance=%v, enabled=%v", h, got, enabled)
					}
					chief.Prompted()
					chief.Reply("Ready. <!-- attn:state=idle -->")
					if _, err := w.Client().CrewHandoff(protocol.SessionID(id), "Rest.", false, protocol.CrewSessionCloseSleep, ""); err != nil {
						t.Fatal(err)
					}
				}
				member := []string{"alder", "keel", "trellis"}[i]
				day := w.Launched(string(wakeCrew(t, w.Client(), member, "codex").SessionID))
				if got := strings.Contains(launchGuidance(t, day), "The user can send you messages from Quick Capture"); got != enabled {
					t.Fatalf("crew Quick Capture guidance=%v, enabled=%v", got, enabled)
				}
			}
		})
	}
}

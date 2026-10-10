package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/hooks"
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
	case fakeagent.Copilot:
		dirs, _ := envEntry(run.Env, "COPILOT_CUSTOM_INSTRUCTIONS_DIRS")
		list := strings.Split(dirs, ",")
		files, _ := filepath.Glob(filepath.Join(list[len(list)-1], "*.instructions.md"))
		if len(files) != 1 {
			t.Fatalf("copilot's last instructions dir in %q holds %d instructions files", dirs, len(files))
		}
		content, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	t.Fatalf("no guidance channel for %s", run.Harness)
	return ""
}

func TestEachHarnessReadsAttnsGuidanceAndOnlyTheHooklessOneRecordsItsOwnPullRequests(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex, fakeagent.Copilot)
	t.Setenv("COPILOT_CUSTOM_INSTRUCTIONS_DIRS", "/home/me/instructions")
	app := w.App()
	yolo := map[fakeagent.Harness]string{
		fakeagent.Claude:  "--dangerously-skip-permissions",
		fakeagent.Codex:   "--dangerously-bypass-approvals-and-sandbox",
		fakeagent.Copilot: "--yolo",
	}
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex, fakeagent.Copilot} {
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
		if recordsPRs := strings.Contains(guidance, "attn pr record"); recordsPRs != (h == fakeagent.Copilot) {
			t.Errorf("%s is told to record its own pull requests: %t, want only the hookless copilot", h, recordsPRs)
		}
		if h == fakeagent.Copilot {
			if dirs, _ := envEntry(run.Env, "COPILOT_CUSTOM_INSTRUCTIONS_DIRS"); !strings.HasPrefix(dirs, "/home/me/instructions,") {
				t.Errorf("copilot's instructions dirs are %q, want the user's own kept ahead of attn's", dirs)
			}
			if strings.HasPrefix(strings.TrimSpace(guidance), "---") {
				t.Errorf("copilot's attn instructions open with frontmatter, which scopes them away:\n%s", guidance)
			}
		}
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
				{"the chief", chief, "You are this profile's Chief", "ATTN_CHIEF_GUIDANCE", "ATTN_AGENT_GUIDANCE"},
				{"a worker", worker, agentGuidanceLead, "ATTN_AGENT_GUIDANCE", "ATTN_CHIEF_GUIDANCE"},
			} {
				guidance := launchGuidance(t, tc.run)
				other := agentGuidanceLead
				if tc.guidance == agentGuidanceLead {
					other = "You are this profile's Chief"
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

func TestCodexIsGivenTheWorkflowGuidanceOnlyWhileWorkflowsAreOn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	before := launchGuidance(t, w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("before"))))
	setSetting(t, app, "workflows_enabled", "true")
	after := launchGuidance(t, w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("after"))))
	if strings.Contains(before, hooks.WorkflowTriggerGuidance()) || !strings.Contains(after, hooks.WorkflowTriggerGuidance()) {
		t.Errorf("codex carried the workflow guidance before workflows were on: %t, after: %t",
			strings.Contains(before, hooks.WorkflowTriggerGuidance()), strings.Contains(after, hooks.WorkflowTriggerGuidance()))
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

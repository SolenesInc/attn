package daemon_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func flagValue(argv []string, flag string) (string, bool) {
	i := slices.Index(argv, flag)
	if i < 0 || i+1 >= len(argv) {
		return "", false
	}
	return argv[i+1], true
}

func TestAutoApproveDefaultsOnAndRespectsAnExplicitChoice(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, agent := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
			for _, stored := range []string{"", "false", "true"} {
				t.Run(string(agent)+"/direct="+strconv.FormatBool(direct)+"/stored="+stored, func(t *testing.T) {
					w := newWorld(t, agent)
					app := w.App()
					if direct {
						setSetting(t, app, "direct_agent_launch_enabled", "true")
					}
					if stored != "" {
						setSetting(t, app, "auto_approve_enabled", stored)
					}
					want := stored != "false"
					if got := w.App().Initial.Settings["auto_approve_enabled"]; got != strconv.FormatBool(want) {
						t.Errorf("settings report auto-approve %q, want %t", got, want)
					}
					session := w.Spawn(app, agent, w.Path("shop"))
					run := w.Launched(session)
					mode, _ := flagValue(run.Argv, "--permission-mode")
					auto := mode == "auto"
					if agent == fakeagent.Codex {
						auto = slices.Contains(run.Argv, `approvals_reviewer="auto_review"`)
					}
					if auto != want {
						t.Errorf("%s ran with auto-approve %t, want %t: %q", agent, auto, want, run.Argv)
					}
					t.Logf("stored=%q settings=%t auto-approve flag=%t", stored, want, auto)
				})
			}
		}
	}
}

func TestLaunchChoicesReachTheAgentAsFlagsAndLeaveNoOneShotVariableBehind(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {

			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			if direct {
				setSetting(t, app, "direct_agent_launch_enabled", "true")
			}
			setSetting(t, app, "auto_approve_enabled", "true")

			yolo := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
				m.Label = protocol.Ptr("pricing")
				m.YoloMode = protocol.Ptr(true)
				m.Model = protocol.Ptr("claude-sonnet-5")
				m.Effort = protocol.Ptr("high")
			})
			reviewed := w.Spawn(app, fakeagent.Claude, w.Path("shop"))

			testworld.AwaitSession(app, yolo, func(s protocol.Session) bool { return s.Label == "pricing" })
			yoloRun, reviewedRun := w.Launched(yolo), w.Launched(reviewed)

			if !slices.Contains(yoloRun.Argv, "--dangerously-skip-permissions") {
				t.Errorf("a yolo launch ran claude %q, want it to skip permissions", yoloRun.Argv)
			}
			if model, _ := flagValue(yoloRun.Argv, "--model"); model != "claude-sonnet-5" {
				t.Errorf("a pinned launch ran claude with model %q, want claude-sonnet-5", model)
			}
			if effort, _ := flagValue(yoloRun.Argv, "--effort"); effort != "high" {
				t.Errorf("a pinned launch ran claude with effort %q, want high", effort)
			}
			if mode, _ := flagValue(reviewedRun.Argv, "--permission-mode"); mode != "auto" {
				t.Errorf("with auto-approve on, claude ran with permission mode %q, want auto", mode)
			}
			if _, pinned := flagValue(reviewedRun.Argv, "--model"); pinned {
				t.Errorf("an unpinned launch ran claude %q, want no model pin", reviewedRun.Argv)
			}

			for _, run := range []*fakeagent.Run{yoloRun, reviewedRun} {
				for _, pair := range run.Env {
					key, _, _ := strings.Cut(pair, "=")
					switch key {
					case "ATTN_MODEL", "ATTN_EFFORT", "ATTN_AUTO_APPROVE", "ATTN_TRUST_WORKING_DIRECTORY", "ATTN_AUTO_COMPACT_WINDOW":
						t.Errorf("claude for session %s inherited the one-shot launch variable %s", run.SessionID, pair)
					}
				}
			}

		})
	}
}

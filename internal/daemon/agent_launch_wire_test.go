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
	launches := []struct{ direct, stored string }{
		{"false", ""},
		{"true", ""},
		{"true", "false"},
		{"false", "false"},
		{"false", "true"},
	}
	for _, agent := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(agent), func(t *testing.T) {
			w := newWorld(t, agent)
			app := w.App()
			for _, launch := range launches {
				setSetting(t, app, "direct_agent_launch_enabled", launch.direct)
				if launch.stored != "" {
					setSetting(t, app, "auto_approve_enabled", launch.stored)
				}
				want := launch.stored != "false"
				if got := w.App().Initial.Settings["auto_approve_enabled"]; got != strconv.FormatBool(want) {
					t.Errorf("direct=%s stored=%q: settings report auto-approve %q, want %t", launch.direct, launch.stored, got, want)
				}
				run := w.Launched(w.Spawn(app, agent, w.Path("shop")))
				mode, _ := flagValue(run.Argv, "--permission-mode")
				auto := mode == "auto"
				if agent == fakeagent.Codex {
					auto = w.CodexServer().Permissions(run.ConversationID).ApprovalsReviewer == "auto_review"
				}
				if auto != want {
					t.Errorf("direct=%s stored=%q: %s ran with auto-approve %t, want %t: %q", launch.direct, launch.stored, agent, auto, want, run.Argv)
				}
			}
		})
	}
}

func TestClaudeLaunchesAndReloadsWithItsAgentViewOff(t *testing.T) {
	t.Setenv("CLAUDE_CODE_DISABLE_AGENT_VIEW", "")
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", strconv.FormatBool(direct))
			session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
			assertAgentViewOff := func(launch string) {
				t.Helper()
				if run := w.Launched(session); !slices.Contains(run.Env, "CLAUDE_CODE_DISABLE_AGENT_VIEW=1") {
					t.Errorf("claude's %s can move its conversation into Claude's background daemon, out of this terminal", launch)
				}
			}
			assertAgentViewOff("launch")
			launchIntentReload(t, app, session)
			assertAgentViewOff("reload")
		})
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
					case "ATTN_MODEL", "ATTN_EFFORT", "ATTN_AUTO_APPROVE", "ATTN_AUTO_COMPACT_WINDOW":
						t.Errorf("claude for session %s inherited the one-shot launch variable %s", run.SessionID, pair)
					}
				}
			}

		})
	}
}

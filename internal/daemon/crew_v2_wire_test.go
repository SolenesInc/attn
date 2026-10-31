package daemon_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCrewV2ReachesPlainAndSharedCodexWithoutHarnessMemory(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, direct := range []bool{false, true} {
			t.Run(fmt.Sprintf("shared=%t/direct=%t", shared, direct), func(t *testing.T) {
				w := newCrewWorld(t, fakeagent.Codex)
				cli, app := w.Client(), w.App()
				setSetting(t, app, "crew_v2_enabled", "true")
				setSetting(t, app, "codex_shared_enabled", fmt.Sprint(shared))
				setSetting(t, app, "direct_agent_launch_enabled", fmt.Sprint(direct))
				setCrew(t, cli, "keel", protocol.CrewSetMessage{Agent: protocol.Ptr("codex")})
				day := wakeCrew(t, cli, "keel", "")
				if shared {
					app.AwaitScreen(string(day.SessionID), "Trust this folder?")
					app.TypeLine(string(day.SessionID), "y")
				}
				t.Cleanup(func() {
					if t.Failed() {
						if peek, err := cli.AgentPeek(string(day.SessionID)); err == nil && peek.Screen != nil {
							t.Log(peek.Screen.Text)
						}
					}
				})
				run := w.Launched(string(day.SessionID))
				if got, want := run.Prompted(), prompts.RenderText("crew", "wake", prompts.Values{"crew_v2_enabled": "true"}); got != want {
					t.Fatalf("v2 wake prompt = %q, want %q", got, want)
				}
				if shared {
					testworld.AwaitSession(app, string(day.SessionID), func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
					config := w.CodexServer().MemoryConfig(run.ConversationID)
					if config["features.memories"] != false || config["memories.generate_memories"] != false {
						t.Fatalf("shared Codex memory config = %v", config)
					}
					if instructions := w.CodexServer().Instructions(run.ConversationID); !strings.Contains(instructions, "## How you work") {
						t.Fatalf("shared Codex missed the crew priming: %s", instructions)
					}
				} else {
					argv := strings.Join(run.Argv, "\n")
					for _, want := range []string{"features.memories=false", "memories.generate_memories=false", "## How you work"} {
						if !strings.Contains(argv, want) {
							t.Fatalf("plain Codex launch missed %q: %s", want, argv)
						}
					}
				}
			})
		}
	}
}

func TestCrewV2DisablesClaudeMemoryAtEachLaunch(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprint(direct), func(t *testing.T) {
			w := newCrewWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			setSetting(t, app, "direct_agent_launch_enabled", fmt.Sprint(direct))
			for _, enabled := range []bool{false, true} {
				setSetting(t, app, "crew_v2_enabled", fmt.Sprint(enabled))
				day := wakeCrew(t, cli, "trellis", "")
				run := w.Launched(string(day.SessionID))
				if got, want := run.Prompted(), prompts.RenderText("crew", "wake", prompts.Values{"crew_v2_enabled": fmt.Sprint(enabled)}); got != want {
					t.Fatalf("v2=%t wake prompt = %q, want %q", enabled, got, want)
				}
				if got := strings.Contains(strings.Join(run.Argv, "\n"), "## How you work"); got != enabled {
					t.Fatalf("v2=%t Claude received operating model=%t", enabled, got)
				}
				if got := slices.Contains(run.Env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"); got != enabled {
					t.Fatalf("v2=%t Claude memory disabled=%t", enabled, got)
				}
				crewHandoff(t, cli, string(day.SessionID), "The next launch reads the setting again.", false)
			}
		})
	}
}

func TestCrewV2SuccessorGetsTheOperatingModelAndDiscoveryPrompt(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	cli := w.Client()
	setSetting(t, w.App(), "crew_v2_enabled", "true")
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID)).Prompted()
	switchCrewHarness(t, cli, "trellis", "codex")
	handed := crewHandoff(t, cli, string(day.SessionID), "Follow s-example to the current work.", false)
	next := w.Launched(string(protocol.Deref(handed.SessionID)))
	if got, want := next.Prompted(), prompts.RenderText("crew", "successor", prompts.Values{"crew_v2_enabled": "true"}); got != want {
		t.Fatalf("v2 successor prompt = %q, want %q", got, want)
	}
	for _, want := range []string{"## How you work", "Follow s-example to the current work."} {
		if !strings.Contains(strings.Join(next.Argv, "\n"), want) {
			t.Fatalf("successor launch missed %q", want)
		}
	}
}

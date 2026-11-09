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

func TestCrewReachesSharedCodexWithoutHarnessMemory(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("direct=%t", direct), func(t *testing.T) {
			w := newCrewWorld(t, fakeagent.Codex)
			cli, app := w.Client(), w.App()
			setSetting(t, app, "direct_agent_launch_enabled", fmt.Sprint(direct))
			setCrew(t, cli, "keel", protocol.CrewSetMessage{Agent: protocol.Ptr("codex")})
			day := wakeCrew(t, cli, "keel", "")
			t.Cleanup(func() {
				if t.Failed() {
					if peek, err := cli.AgentPeek(string(day.SessionID)); err == nil && peek.Screen != nil {
						t.Log(peek.Screen.Text)
					}
				}
			})
			run := w.Launched(string(day.SessionID))
			if got, want := run.Prompted(), prompts.RenderText("crew", "wake", nil); got != want {
				t.Fatalf("wake prompt = %q, want %q", got, want)
			}
			testworld.AwaitSession(app, string(day.SessionID), func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			config := w.CodexServer().MemoryConfig(run.ConversationID)
			if config["features.memories"] != false || config["memories.generate_memories"] != false {
				t.Fatalf("shared Codex memory config = %v", config)
			}
			if instructions := w.CodexServer().Instructions(run.ConversationID); !strings.Contains(instructions, "## How you work") {
				t.Fatalf("shared Codex missed the crew priming: %s", instructions)
			}
		})
	}
}

func TestCrewDisablesClaudeMemoryAtLaunch(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprint(direct), func(t *testing.T) {
			w := newCrewWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			setSetting(t, app, "direct_agent_launch_enabled", fmt.Sprint(direct))
			woken := wakeCrew(t, cli, "trellis", "")
			run := w.Launched(string(woken.SessionID))
			if got, want := run.Prompted(), prompts.RenderText("crew", "wake", nil); got != want {
				t.Fatalf("wake prompt = %q, want %q", got, want)
			}
			if !strings.Contains(strings.Join(run.Argv, "\n"), "## How you work") {
				t.Fatalf("Claude missed the operating model")
			}
			if !slices.Contains(run.Env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1") {
				t.Fatalf("Claude launched with memory on: %q", run.Env)
			}
			agent := w.Spawn(app, fakeagent.Claude, w.Path("work"))
			if slices.Contains(w.Launched(agent).Env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1") {
				t.Fatal("an ordinary agent launched with memory off")
			}
		})
	}
}

func TestCrewSuccessorGetsTheOperatingModelAndDiscoveryPrompt(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	cli := w.Client()
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID)).Prompted()
	switchCrewHarness(t, cli, "trellis", "codex")
	handed := crewHandoff(t, cli, string(day.SessionID), "Follow s-example to the current work.", false)
	next := w.Launched(string(protocol.Deref(handed.SessionID)))
	if got, want := next.Prompted(), prompts.RenderText("crew", "successor", nil); got != want {
		t.Fatalf("successor prompt = %q, want %q", got, want)
	}
	for _, want := range []string{"## How you work", "Follow s-example to the current work."} {
		if instructions := w.CodexServer().Instructions(next.ConversationID); !strings.Contains(instructions, want) {
			t.Fatalf("successor launch missed %q", want)
		}
	}
}

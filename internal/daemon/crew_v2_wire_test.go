package daemon_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
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
				run.Prompted()
				if shared {
					testworld.AwaitSession(app, string(day.SessionID), func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
					config := w.CodexServer().MemoryConfig(run.ConversationID)
					if config["features.memories"] != false || config["memories.generate_memories"] != false {
						t.Fatalf("shared Codex memory config = %v", config)
					}
					if instructions := w.CodexServer().Instructions(run.ConversationID); !strings.Contains(instructions, "crew member of this attn home") {
						t.Fatalf("shared Codex missed the crew priming: %s", instructions)
					}
				} else {
					argv := strings.Join(run.Argv, "\n")
					for _, want := range []string{"features.memories=false", "memories.generate_memories=false", "crew member of this attn home"} {
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
				run.Prompted()
				if got := slices.Contains(run.Env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"); got != enabled {
					t.Fatalf("v2=%t Claude memory disabled=%t", enabled, got)
				}
				crewHandoff(t, cli, string(day.SessionID), "The next launch reads the setting again.", false, protocol.CrewDayCloseSleep)
			}
		})
	}
}

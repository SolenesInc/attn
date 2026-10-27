package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClosedCrewDaysKeepTheirTranscripts(t *testing.T) {
	for _, agent := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex, fakeagent.Pi} {
		t.Run(string(agent), func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(agent))
			writeCharter(t, s, "trellis")
			s.Vars = append(s.Vars, "ATTN_CONVERSATION_KEEP_GRACE_DAYS=0")
			s.Start()
			app := s.App()
			awaitAgentAvailable(app, agent)
			requireStdout(t, s.Attn("settings", "set", "crew_v2_enabled", "true"), "crew_v2_enabled")
			requireStdout(t, s.Attn("crew", "set", "trellis", "--agent", string(agent)), string(agent))
			var day protocol.CrewWakeResult
			s.Attn("crew", "wake", "trellis", "--json").JSON(t, &day)
			run := s.Launched(string(day.SessionID))
			run.Prompted()
			converse(app, run, string(day.SessionID), "Remember the day's decision.", "Keep the reasons beside the decision.")
			var handoff protocol.CrewHandoffResult
			closed := s.Run(testworld.Invocation{Session: string(day.SessionID), Args: []string{"handoff", "--sleep", "-m", "Dear successor, keep the reasons.", "--json"}})
			closed.JSON(t, &handoff)
			if closed.Code != 0 || handoff.SessionID != nil {
				t.Fatalf("close crew day: %+v, %+v", closed, handoff)
			}
			testworld.AwaitTaskDone(app, "conversation_keep")
			target := string(day.SessionID)
			s.Stop()
			s.Start()
			if agent == fakeagent.Claude {
				list := s.Attn("conversation", "list")
				requireStdout(t, list, "1 kept", "crew session trellis "+target)
				fmt.Printf("$ attn conversation list\n%s", list.Stdout)
				paths, err := filepath.Glob(filepath.Join(s.Dir, "toolhome", ".claude", "projects", "*", run.ConversationID+".jsonl"))
				if err != nil || len(paths) != 1 {
					t.Fatalf("Claude source transcript: %v, %v", paths, err)
				}
				if err := os.Remove(paths[0]); err != nil {
					t.Fatal(err)
				}
				requireStdout(t, s.Attn("settings", "set", "crew_v2_enabled", "false"), "crew_v2_enabled")
				for _, pass := range []string{"v2-off", "past-grace"} {
					keepPassAfterAnotherSessionCloses(t, s, pass)
				}
				list = s.Attn("conversation", "list")
				requireStdout(t, list, "1 kept", "crew session trellis "+target)
				fmt.Printf("$ attn conversation list (v2 disabled, source removed, two keep passes)\n%s", list.Stdout)
				refused := s.Attn("conversation", "forget", target)
				if refused.Code == 0 || !strings.Contains(refused.Stderr, "trellis "+target) {
					t.Fatalf("forget a crew day's transcript: %+v", refused)
				}
			}
			read := s.Attn("session", "transcript", target)
			requireStdout(t, read, "Remember the day's decision.", "Keep the reasons beside the decision.")
			fmt.Printf("$ attn session transcript %s (%s, closed day)\n%s", target, agent, read.Stdout)
			s.Stop()
			s.Start()
			read = s.Attn("session", "transcript", target)
			requireStdout(t, read, "Keep the reasons beside the decision.")
			requireStdout(t, s.Attn("settings", "set", "crew_v2_enabled", "false"), "crew_v2_enabled")
			app = s.App()
			awaitAgentAvailable(app, agent)
			var withoutV2 protocol.CrewWakeResult
			s.Attn("crew", "wake", "trellis", "--json").JSON(t, &withoutV2)
			next := s.Launched(string(withoutV2.SessionID))
			next.Prompted()
			converse(app, next, string(withoutV2.SessionID), "Another session with v2 off.", "This transcript has no letter reference.")
			closed = s.Run(testworld.Invocation{Session: string(withoutV2.SessionID), Args: []string{"handoff", "--sleep", "-m", "Filed with v2 off.", "--json"}})
			closed.JSON(t, &handoff)
			testworld.AwaitTaskDone(app, "conversation_keep")
			var kept protocol.KeptConversationListResult
			s.Attn("conversation", "list", "--json").JSON(t, &kept)
			for _, row := range kept.Rows {
				for _, session := range row.SessionIds {
					if session == withoutV2.SessionID {
						t.Fatalf("v2-off letter kept its transcript: %+v", row)
					}
				}
			}
			fmt.Println("PASS: letter filed with crew_v2_enabled=false records no transcript reference")
		})
	}
}

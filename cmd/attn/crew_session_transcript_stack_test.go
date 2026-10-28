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

func TestClosedCrewSessionsKeepTheirTranscripts(t *testing.T) {
	for _, agent := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex, fakeagent.Pi} {
		t.Run(string(agent), func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(agent))
			writeCharter(t, s, "trellis")
			s.Vars = append(s.Vars, "ATTN_CONVERSATION_KEEP_GRACE_DAYS=0")
			s.Start()
			app := s.App()
			awaitAgentAvailable(app, agent)
			requireStdout(t, s.Attn("settings", "set", "crew_memory_enabled", "true"), "crew_memory_enabled")
			requireStdout(t, s.Attn("crew", "set", "trellis", "--agent", string(agent)), string(agent))
			var day protocol.CrewWakeResult
			s.Attn("crew", "wake", "trellis", "--json").JSON(t, &day)
			run := s.Launched(string(day.SessionID))
			run.Prompted()
			converse(app, run, string(day.SessionID), "Remember the session's decision.", "Keep the reasons beside the decision.")
			var handoff protocol.CrewHandoffResult
			closed := s.Run(testworld.Invocation{Session: string(day.SessionID), Args: []string{"handoff", "--sleep", "--summary", "Kept the decision and its reasons.", "-m", "Dear successor, keep the reasons.", "--json"}})
			closed.JSON(t, &handoff)
			if closed.Code != 0 || handoff.SessionID != nil || handoff.EntryID == nil {
				t.Fatalf("close crew session: %+v, %+v", closed, handoff)
			}
			testworld.AwaitTaskDone(app, "conversation_keep")
			zoom := s.Attn("memory", "zoom", *handoff.EntryID, "--member", "trellis")
			requireStdout(t, zoom, "attn session transcript "+string(day.SessionID))
			fmt.Printf("$ attn memory zoom %s --member trellis\n%s", *handoff.EntryID, zoom.Stdout)
			_, target, ok := strings.Cut(zoom.Stdout, "Transcript: attn session transcript ")
			target = strings.TrimSpace(target)
			if !ok || target != string(day.SessionID) {
				t.Fatalf("session line session: %q", target)
			}
			if agent == fakeagent.Claude {
				list := s.Attn("conversation", "list")
				requireStdout(t, list, "1 kept", "crew session Trellis #"+*handoff.EntryID)
				fmt.Printf("$ attn conversation list\n%s", list.Stdout)
				paths, err := filepath.Glob(filepath.Join(s.Dir, "toolhome", ".claude", "projects", "*", run.ConversationID+".jsonl"))
				if err != nil || len(paths) != 1 {
					t.Fatalf("Claude source transcript: %v, %v", paths, err)
				}
				if err := os.Remove(paths[0]); err != nil {
					t.Fatal(err)
				}
				requireStdout(t, s.Attn("settings", "set", "crew_memory_enabled", "false"), "crew_memory_enabled")
				for _, pass := range []string{"memory-off", "past-grace"} {
					keepPassAfterAnotherSessionCloses(t, s, pass)
				}
				list = s.Attn("conversation", "list")
				requireStdout(t, list, "1 kept", "crew session Trellis #"+*handoff.EntryID)
				fmt.Printf("$ attn conversation list (memory disabled, source removed, two keep passes)\n%s", list.Stdout)
				refused := s.Attn("conversation", "forget", target)
				if refused.Code == 0 || !strings.Contains(refused.Stderr, "trellis #"+*handoff.EntryID) {
					t.Fatalf("forget a crew session's transcript: %+v", refused)
				}
			}
			read := s.Attn("session", "transcript", target)
			requireStdout(t, read, "Remember the session's decision.", "Keep the reasons beside the decision.")
			fmt.Printf("$ attn session transcript %s (%s, closed session)\n%s", target, agent, read.Stdout)
			s.Stop()
			s.Start()
			read = s.Attn("session", "transcript", target)
			requireStdout(t, read, "Keep the reasons beside the decision.")
		})
	}
}

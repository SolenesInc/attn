package main_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestForgetCrewLetterTranscriptRequiresConsent(t *testing.T) {
	for _, partialFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial-failure=%v", partialFailure), func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			writeCharter(t, s, "trellis")
			writeCharter(t, s, "keel")
			s.Start()
			app := s.App()
			awaitAgentAvailable(app, fakeagent.Claude)
			requireStdout(t, s.Attn("settings", "set", "crew_v2_enabled", "true"), "crew_v2_enabled")
			var day protocol.CrewWakeResult
			s.Attn("crew", "wake", "trellis", "--json").JSON(t, &day)
			run := s.Launched(string(day.SessionID))
			run.Prompted()
			converse(app, run, string(day.SessionID), "Keep the reasons.", "The letter and the transcript have separate lifetimes.")
			seed := plant(t, s, "Still using this transcript")
			requireStdout(t, s.Attn("seed", "tend", seed.ID, "--session", string(day.SessionID)), seed.ID)
			var handoff protocol.CrewHandoffResult
			s.Run(testworld.Invocation{Session: string(day.SessionID), Args: []string{"handoff", "-m", "Dear successor, keep the reasons.", "--json"}}).JSON(t, &handoff)
			testworld.AwaitTaskDone(app, "conversation_keep")
			archive := filepath.Join(s.Dir, "conversations", "claude", run.ConversationID+".tar.zst")
			otherLetter := filepath.Join(s.Dir, "crew", "keel", "handoffs", strings.Replace(filepath.Base(handoff.Path), "trellis", "keel", 1))
			if err := os.MkdirAll(filepath.Dir(otherLetter), 0755); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"", ".json"} {
				body, err := os.ReadFile(handoff.Path + suffix)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(otherLetter+suffix, body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := make(map[string][]byte)
			for _, path := range []string{handoff.Path, handoff.Path + ".json", otherLetter, otherLetter + ".json", archive} {
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = body
			}
			unchanged := func() {
				t.Helper()
				for path, original := range before {
					if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
						t.Fatalf("refusal changed %s: %v", path, err)
					}
				}
			}
			blocked := s.Attn("conversation", "forget", run.ConversationID, "--unlink-crew-letters")
			if blocked.Code == 0 || !strings.Contains(blocked.Stderr, seed.ID) {
				t.Fatalf("consent must not bypass open seeds: %+v", blocked)
			}
			unchanged()
			requireStdout(t, s.Attn("seed", "wither", seed.ID, "-m", "finished"), seed.ID)
			testworld.AwaitTaskDone(app, "conversation_keep")
			refused := s.Attn("conversation", "forget", run.ConversationID)
			if refused.Code == 0 || !strings.Contains(refused.Stderr, "trellis "+filepath.Base(handoff.Path)) || !strings.Contains(refused.Stderr, "--unlink-crew-letters") || !strings.Contains(refused.Stderr, "keel "+filepath.Base(otherLetter)) {
				t.Fatalf("refusal must name the letter and consent flag: %+v", refused)
			}
			fmt.Printf("$ attn conversation forget %s\n%s", run.ConversationID, refused.Stderr)
			unchanged()
			if partialFailure {
				dir := filepath.Dir(handoff.Path)
				if err := os.Chmod(dir, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
				failed := s.Attn("conversation", "forget", run.ConversationID, "--unlink-crew-letters")
				if failed.Code == 0 || !strings.Contains(failed.Stderr, "unlinked crew letters: keel "+filepath.Base(otherLetter)) || !strings.Contains(failed.Stderr, "attn's copy remains") || !strings.Contains(failed.Stderr, "--unlink-crew-letters") {
					t.Fatalf("partial failure must report progress and retry: %+v", failed)
				}
				fmt.Printf("$ attn conversation forget (one record cannot be removed)\n%s", failed.Stderr)
				if _, err := os.Stat(otherLetter + ".json"); !os.IsNotExist(err) {
					t.Fatalf("first record must already be removed: %v", err)
				}
				for _, path := range []string{handoff.Path, handoff.Path + ".json", otherLetter, archive} {
					if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, before[path]) {
						t.Fatalf("partial failure changed %s: %v", path, err)
					}
				}
				var remaining protocol.KeptConversationListResult
				s.Attn("conversation", "list", "--json").JSON(t, &remaining)
				if len(remaining.Rows) != 1 || remaining.Rows[0].Kept == nil || remaining.Rows[0].Kept.DeletedAt != nil || len(remaining.Rows[0].CrewLetters) != 1 || remaining.Rows[0].CrewLetters[0].Member != "trellis" {
					t.Fatalf("partial progress must be visible: %+v", remaining)
				}
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			forgot := s.Attn("conversation", "forget", run.ConversationID, "--unlink-crew-letters")
			requireStdout(t, forgot, "deleted attn's copy", "trellis "+filepath.Base(handoff.Path), "letters are unchanged")
			fmt.Printf("$ attn conversation forget %s --unlink-crew-letters\n%s", run.ConversationID, forgot.Stdout)
			testworld.AwaitTaskDone(app, "conversation_keep")
			keepPassAfterAnotherSessionCloses(t, s, "after-forget")
			for _, path := range []string{handoff.Path + ".json", otherLetter + ".json", archive} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("forgotten file survived the next keep pass: %s: %v", path, err)
				}
			}
			for _, path := range []string{handoff.Path, otherLetter} {
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, before[path]) {
					t.Fatalf("forget changed the letter %s: %v", path, err)
				}
			}
			var listed protocol.KeptConversationListResult
			s.Attn("conversation", "list", "--all", "--json").JSON(t, &listed)
			if len(listed.Rows) != 1 || listed.Rows[0].Kept == nil || listed.Rows[0].Kept.DeletedAt == nil || len(listed.Rows[0].CrewLetters) != 0 {
				t.Fatalf("forgotten copy must stay deleted without letter references: %+v", listed)
			}
			fmt.Println("PASS: consent removes the session records and archive, preserves the letters, and the next keep pass leaves the copy deleted")
		})
	}
}

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
			closed := s.Run(testworld.Invocation{Session: string(day.SessionID), Args: []string{"handoff", "-m", "Dear successor, keep the reasons.", "--json"}})
			closed.JSON(t, &handoff)
			if closed.Code != 0 || handoff.SessionID != nil {
				t.Fatalf("close crew day: %+v, %+v", closed, handoff)
			}
			testworld.AwaitTaskDone(app, "conversation_keep")
			target := string(day.SessionID)
			letter := filepath.Base(handoff.Path)
			s.Stop()
			s.Start()
			if agent == fakeagent.Claude {
				list := s.Attn("conversation", "list")
				requireStdout(t, list, "1 kept", "crew letter trellis "+letter)
				fmt.Printf("$ attn conversation list\n%s", list.Stdout)
				var listed protocol.KeptConversationListResult
				s.Attn("conversation", "list", "--json").JSON(t, &listed)
				if len(listed.Rows) != 1 || len(listed.Rows[0].CrewLetters) != 1 || listed.Rows[0].CrewLetters[0].Filename != letter || listed.Rows[0].CrewLetters[0].OccurredAt.IsZero() {
					t.Fatalf("letter reference = %+v, want the filed letter %s with its date", listed, letter)
				}
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
				requireStdout(t, list, "1 kept", "crew letter trellis "+letter)
				fmt.Printf("$ attn conversation list (v2 disabled, source removed, two keep passes)\n%s", list.Stdout)
				refused := s.Attn("conversation", "forget", target)
				if refused.Code == 0 || !strings.Contains(refused.Stderr, "trellis "+letter) {
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
			closed = s.Run(testworld.Invocation{Session: string(withoutV2.SessionID), Args: []string{"handoff", "-m", "Filed with v2 off.", "--json"}})
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

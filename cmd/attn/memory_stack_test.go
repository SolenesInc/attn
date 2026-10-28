package main_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCrewMemoryCrashRetryRepairsOneSessionLineAndPreservesSleep(t *testing.T) {
	for _, point := range []string{"crew-memory-letter-recorded", "crew-memory-session-appended"} {
		t.Run(point, func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			writeCharter(t, s, "trellis")
			s.StartCrashingAt(point)
			requireStdout(t, s.Attn("settings", "set", "crew_memory_enabled", "true"), "crew_memory_enabled")
			var day protocol.CrewWakeResult
			s.Attn("crew", "wake", "trellis", "--json").JSON(t, &day)
			s.Launched(string(day.SessionID)).Prompted()
			invoke := func(args ...string) testworld.Result {
				return s.Run(testworld.Invocation{Session: string(day.SessionID), Args: args})
			}
			requireStdout(t, invoke("memory", "add", "A lesson that survives a crash."), "#0")
			failed := invoke("handoff", "--sleep", "--summary", "The crash recovery day.", "-m", "Dear successor, keep the lesson.")
			if failed.Code == 0 {
				t.Fatalf("handoff did not crash at %s: %+v", point, failed)
			}
			s.AwaitCrash()
			s.Start()
			var first protocol.CrewHandoffResult
			invoke("handoff", "--retry", "--json").JSON(t, &first)
			if protocol.Deref(first.EntryID) != "1" || protocol.Deref(first.PendingSummaries) != 1 {
				t.Fatalf("recovered handoff = %+v", first)
			}
			var again protocol.CrewHandoffResult
			invoke("handoff", "--retry", "--json").JSON(t, &again)
			if first.Path != again.Path || protocol.Deref(again.EntryID) != "1" {
				t.Fatalf("second retry filed another day: %+v, %+v", first, again)
			}
			var read protocol.CrewMemoryReadResult
			invoke("memory", "recall", ".", "--json").JSON(t, &read)
			if protocol.Deref(read.Matches) != 2 {
				t.Fatalf("retry left %d entries, want the lesson and one session line", protocol.Deref(read.Matches))
			}
			done := s.Run(testworld.Invocation{Session: string(day.SessionID), Args: []string{"memory", "summarize", "-", "--json"}, Stdin: "0-1 Keep the lesson from the crash recovery day.\n"})
			var summarized protocol.CrewMemorySummarizeResult
			done.JSON(t, &summarized)
			if summarized.Handoff == nil || protocol.Deref(summarized.Handoff.Outcome) != protocol.CrewSessionCloseSleep || summarized.Handoff.SessionID != nil {
				t.Fatalf("summary lost the requested sleep: %+v", summarized)
			}
			if binding := crewRoster(t, s)["trellis"].BindingSession; binding != nil {
				t.Fatalf("sleep left a successor: %s", *binding)
			}
			fmt.Printf("%s: one session line after two retries; final summary honored --sleep.\n", point)
		})
	}
}

func TestCrewMemoryCLIReportsItsRecallCapAndPreservesRecentMatches(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "trellis")
	s.Start()
	s.Attn("settings", "set", "crew_memory_enabled", "true")
	var day protocol.CrewWakeResult
	s.Attn("crew", "wake", "trellis", "--json").JSON(t, &day)
	s.Launched(string(day.SessionID)).Prompted()
	cli := s.Client()
	for i := 0; i < 96; i++ {
		if _, err := cli.MemoryAdd(day.SessionID, fmt.Sprintf("lesson %d %s", i, strings.Repeat("x", 250))); err != nil {
			t.Fatal(err)
		}
	}
	result := s.Run(testworld.Invocation{Session: string(day.SessionID), Args: []string{"memory", "recall", "lesson"}})
	requireStdout(t, result, "of 96 matches", "left out by the 20000-byte entry output cap", "Narrow the regex", "lesson 95")
	if strings.Contains(result.Stdout, "lesson 0 ") {
		t.Fatal("the output cap preserved the oldest match")
	}
}

func TestCrewMemoryInterruptedRewriteCanFinishWhenRetried(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "trellis")
	s.StartCrashingAt("crew-memory-summary-invalidated")
	s.Attn("settings", "set", "crew_memory_enabled", "true")
	var day protocol.CrewWakeResult
	s.Attn("crew", "wake", "trellis", "--json").JSON(t, &day)
	s.Launched(string(day.SessionID)).Prompted()
	cli := s.Client()
	for i := 0; i < 4; i++ {
		if _, err := cli.MemoryAdd(day.SessionID, fmt.Sprintf("Lesson %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cli.MemorySummarize(day.SessionID, []protocol.CrewMemoryAnswer{{Range: "0-1", Text: "Incorrect claim."}, {Range: "2-3", Text: "Other lessons."}, {Range: "0-3", Text: "Incorrect inherited claim."}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.MemoryRewrite(day.SessionID, "0-1"); err == nil {
		t.Fatal("rewrite did not crash between levels")
	}
	s.AwaitCrash()
	s.Start()
	cli = s.Client()
	if _, err := cli.MemoryRewrite(day.SessionID, "0-1"); err != nil {
		t.Fatalf("retry refused an interrupted rewrite: %v", err)
	}
	zoom, err := cli.MemoryZoom(day.SessionID, "", "", "0-3")
	if err != nil || len(zoom.Lines) != 4 || strings.Contains(strings.Join(zoom.Lines, "\n"), "Incorrect") {
		t.Fatalf("rewrite retained a mistaken summary: %+v, %v", zoom, err)
	}
}

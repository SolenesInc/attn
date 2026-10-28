package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCrewMemoryClosureWaitsAndHonorsTheRequestedClose(t *testing.T) {
	for _, close := range []protocol.CrewSessionClose{protocol.CrewSessionCloseSleep, protocol.CrewSessionCloseNap} {
		t.Run(string(close), func(t *testing.T) {
			w := newCrewWorld(t, fakeagent.Claude)
			cli := w.Client()
			setSetting(t, w.App(), "crew_memory_enabled", "true")
			day := wakeCrew(t, cli, "trellis", "")
			t.Cleanup(func() {
				if t.Failed() {
					peek, err := cli.AgentPeek(string(day.SessionID))
					t.Logf("failed day screen: %+v (%v)", peek, err)
					if peek != nil && peek.Screen != nil {
						t.Log(peek.Screen.Text)
					}
				}
			})
			run := w.Launched(string(day.SessionID))
			if !strings.Contains(strings.Join(run.Env, "\n"), "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1") {
				t.Fatalf("Claude memory not disabled: %q", run.Env)
			}
			first := crewPriming(t, cli, string(day.SessionID))
			if !strings.Contains(first, "Before your first `attn memory add`") || !strings.Contains(first, "binding check cannot distinguish them") {
				t.Fatalf("first memory wake lacks import or subagent guidance:\n%s", first)
			}
			added, err := cli.MemoryAdd(day.SessionID, "The user wants measured limits, with reasons.")
			if err != nil || added.ID != "0" {
				t.Fatalf("add = %+v, %v", added, err)
			}
			_, err = cli.CrewHandoff(day.SessionID, "The letter.", false, close, "")
			crewErrorContains(t, err, "--summary", "280")
			handoff, err := cli.CrewHandoff(day.SessionID, "The letter.", false, close, "Measured limits, with the reasons kept.")
			if err != nil || protocol.Deref(handoff.PendingSummaries) != 1 || protocol.Deref(handoff.EntryID) != "1" || handoff.SessionID != nil {
				t.Fatalf("pending handoff = %+v, %v", handoff, err)
			}
			if protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession) != day.SessionID {
				t.Fatal("the day closed while summaries were pending")
			}
			retry, err := cli.CrewHandoff(day.SessionID, "", true, "", "")
			if err != nil || protocol.Deref(retry.EntryID) != "1" || retry.Path != handoff.Path {
				t.Fatalf("retry = %+v, %v", retry, err)
			}
			read, err := cli.MemoryRecall(day.SessionID, "", "", "Measured|limits")
			if err != nil || protocol.Deref(read.Matches) != 2 {
				t.Fatalf("recall = %+v, %v", read, err)
			}
			zoom, err := cli.MemoryZoom(day.SessionID, "", "", "1")
			if err != nil || protocol.Deref(zoom.Letter) != handoff.Path || protocol.Deref(zoom.SessionID) != day.SessionID {
				t.Fatalf("day references = %+v, %v", zoom, err)
			}
			ready, err := cli.MemorySummarize(day.SessionID, nil)
			if err != nil || len(ready.Blocks) != 1 || ready.Blocks[0].Range != "0-1" || len(ready.Blocks[0].Lines) != 2 {
				t.Fatalf("ready = %+v, %v", ready, err)
			}
			before := crewSessionCount(t, cli)
			done, err := cli.MemorySummarize(day.SessionID, []protocol.CrewMemoryAnswer{{Range: "0-1", Text: "The user wants measured limits and their reasons."}})
			if err != nil || done.PendingSummaries != 0 || done.Handoff == nil || protocol.Deref(done.Handoff.Outcome) != close {
				t.Fatalf("summary turnover = %+v, %v", done, err)
			}
			if close == protocol.CrewSessionCloseSleep {
				if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil || crewSessionCount(t, cli) != before-1 {
					t.Fatalf("sleep woke a successor: %v", binding)
				}
				day = wakeCrew(t, cli, "trellis", "")
			} else {
				day.SessionID = protocol.Deref(done.Handoff.SessionID)
			}
			w.Launched(string(day.SessionID))
			primed := crewPriming(t, cli, string(day.SessionID))
			if !strings.Contains(primed, "#0 ") || !strings.Contains(primed, "measured limits") || strings.Contains(primed, "Before your first") {
				t.Fatalf("next wake lost the memory:\n%s", primed)
			}
		})
	}
}

func TestCrewMemoryBindingAndDisabledSetting(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli, app := w.Client(), w.App()
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	legacy := crewPriming(t, cli, string(day.SessionID))
	_, err := cli.MemoryAdd(day.SessionID, "a lesson")
	crewErrorContains(t, err, "crew_memory_enabled")
	setSetting(t, app, "crew_memory_enabled", "true")
	request := delegateFrom(string(day.SessionID), crewHome(w, "trellis"), "Check a lesson and report on this seed.", fakeagent.Claude)
	request.RequestID = "memory-delegate"
	delegated := testworld.Request(app, request, protocol.EventDelegateResult, func(r protocol.DelegateResultMessage) bool { return protocol.Deref(r.RequestID) == request.RequestID })
	if !delegated.Success {
		t.Fatalf("delegate: %s", protocol.Deref(delegated.Error))
	}
	w.Launched(string(delegated.Result.SessionID)).Prompted()
	_, err = cli.MemoryAdd(delegated.Result.SessionID, "a duplicated lesson")
	crewErrorContains(t, err, "attn seed note", "not bound to a crew member")
	setSetting(t, app, "crew_memory_enabled", "false")
	if got := crewPriming(t, cli, string(day.SessionID)); got != legacy || strings.Contains(got, "## Your memory") {
		t.Fatal("disabled memory changed legacy priming")
	}
	closed, err := cli.CrewHandoff(day.SessionID, "The legacy letter, without --summary.", false, protocol.CrewSessionCloseSleep, "")
	if err != nil || protocol.Deref(closed.Outcome) != protocol.CrewSessionCloseSleep || closed.EntryID != nil {
		t.Fatalf("legacy closure = %+v, %v", closed, err)
	}
}

func TestCrewMemoryReachesPlainAndSharedCodexWithoutHarnessMemory(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, direct := range []bool{false, true} {
			t.Run(fmt.Sprintf("shared=%t/direct=%t", shared, direct), func(t *testing.T) {
				w := newCrewWorld(t, fakeagent.Codex)
				cli, app := w.Client(), w.App()
				setSetting(t, app, "crew_memory_enabled", "true")
				setSetting(t, app, "codex_shared_enabled", fmt.Sprint(shared))
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
				run.Prompted()
				if shared {
					testworld.AwaitSession(app, string(day.SessionID), func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
					config := w.CodexServer().MemoryConfig(run.ConversationID)
					if config["features.memories"] != false || config["memories.generate_memories"] != false {
						t.Fatalf("shared Codex memory config = %v", config)
					}
					if instructions := w.CodexServer().Instructions(run.ConversationID); !strings.Contains(instructions, "## Your memory") {
						t.Fatalf("shared Codex missed the wake view: %s", instructions)
					}
				} else {
					argv := strings.Join(run.Argv, "\n")
					for _, want := range []string{"features.memories=false", "memories.generate_memories=false", "## Your memory"} {
						if !strings.Contains(argv, want) {
							t.Fatalf("plain Codex launch missed %q: %s", want, argv)
						}
					}
				}
			})
		}
	}
}

func TestCrewMemoryUsesRawBlocksThenTheirHalvesAndRepairsCorrections(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	setSetting(t, w.App(), "crew_memory_enabled", "true")
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	for i := 0; i < 31; i++ {
		if _, err := cli.MemoryAdd(day.SessionID, fmt.Sprintf("Lesson %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := cli.CrewHandoff(day.SessionID, "First letter.", false, protocol.CrewSessionCloseNap, "The old session line.")
	if err != nil {
		t.Fatal(err)
	}
	ready, err := cli.MemorySummarize(day.SessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	var answers []protocol.CrewMemoryAnswer
	for _, b := range ready.Blocks {
		if b.Range == "0-31" || len(b.Lines) > 16 {
			t.Fatalf("large block was offered without its halves: %+v", b)
		}
		answers = append(answers, protocol.CrewMemoryAnswer{Range: b.Range, Text: "Summary of " + b.Range})
	}
	ready, err = cli.MemorySummarize(day.SessionID, answers)
	if err != nil || len(ready.Blocks) != 1 || ready.Blocks[0].Range != "0-31" || len(ready.Blocks[0].Lines) != 2 || !strings.Contains(ready.Blocks[0].Lines[0], "Summary of 0-15") {
		t.Fatalf("large block sources = %+v, %v", ready, err)
	}
	corrected, err := cli.CrewHandoff(day.SessionID, "Corrected letter.", false, protocol.CrewSessionCloseSleep, "The corrected session line.")
	if err != nil || protocol.Deref(corrected.EntryID) != protocol.Deref(first.EntryID) || protocol.Deref(corrected.PendingSummaries) <= 1 {
		t.Fatalf("corrected day = %+v, %v", corrected, err)
	}
	zoom, err := cli.MemoryZoom(day.SessionID, "", "", "31")
	if err != nil || !strings.Contains(zoom.Lines[0], "The corrected session line.") || protocol.Deref(zoom.Letter) != corrected.Path {
		t.Fatalf("corrected reference = %+v, %v", zoom, err)
	}
	if _, err := cli.MemoryForget(day.SessionID, 0); err != nil {
		t.Fatal(err)
	}
	read, err := cli.MemoryRecall(day.SessionID, "", "", "^#0 ")
	if err != nil || len(read.Lines) != 0 {
		t.Fatalf("forgotten entry still recalled = %+v, %v", read, err)
	}
}

func TestCrewMemoryImportsBeforeTheFirstSummaryAndSerializesAdds(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	setSetting(t, w.App(), "crew_memory_enabled", "true")
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	if _, err := cli.MemoryAdd(day.SessionID, "Today's lesson."); err != nil {
		t.Fatal(err)
	}
	imported, err := cli.MemoryImport(day.SessionID, []protocol.CrewMemoryImportLine{{Letter: crewHomeLetters["trellis"], Text: "A lesson from an older day."}})
	if err != nil || protocol.Deref(imported.Imported) != 1 {
		t.Fatalf("import = %+v, %v", imported, err)
	}
	zoom, err := cli.MemoryZoom(day.SessionID, "", "", "0")
	if err != nil || zoom.Letter == nil || zoom.SessionID != nil || !strings.Contains(zoom.Lines[0], "older day") {
		t.Fatalf("imported history = %+v, %v", zoom, err)
	}
	if _, err := cli.MemorySummarize(day.SessionID, []protocol.CrewMemoryAnswer{{Range: "0-1", Text: "Older and newer lessons."}}); err != nil {
		t.Fatal(err)
	}
	_, err = cli.MemoryImport(day.SessionID, nil)
	crewErrorContains(t, err, "first summary already exists")
	if _, err := cli.MemoryRewrite(day.SessionID, "0-1"); err != nil {
		t.Fatal(err)
	}
	_, err = cli.MemoryImport(day.SessionID, nil)
	crewErrorContains(t, err, "first summary already exists")
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			added, err := cli.MemoryAdd(day.SessionID, "An independent lesson.")
			if err != nil {
				t.Error(err)
				return
			}
			ids <- added.ID
		})
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("parallel adds reused #%s", id)
		}
		seen[id] = true
	}
	if len(seen) != 12 {
		t.Fatalf("%d adds acknowledged, want 12", len(seen))
	}
}

func TestCrewMemoryMalformedFilesRefuseWakeAndTornTailsRepairOnWrite(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	setSetting(t, w.App(), "crew_memory_enabled", "true")
	path := writeCrewHomeFile(t, w, "trellis", filepath.Join("memory", "log.jsonl"), "broken\n")
	_, err := cli.CrewWake("trellis", "", "")
	crewErrorContains(t, err, path, "line 1")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	if _, err := cli.MemoryAdd(day.SessionID, "Acknowledged."); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"text":"unacknowledged`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = cli.MemoryRecall(day.SessionID, "", "", ".")
	crewErrorContains(t, err, path, "line 2")
	added, err := cli.MemoryAdd(day.SessionID, "After repair.")
	if err != nil || added.ID != "1" {
		t.Fatalf("repair add = %+v, %v", added, err)
	}
}

func TestCrewMemoryReadsLegacyReferencesThroughCorrections(t *testing.T) {
	for _, tc := range []struct{ name, reference string }{
		{"legacy", `"day":{"letter":%q,"session":"previous-session"}`},
		{"session", `"session":{"letter":%q,"id":"previous-session"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newCrewWorld(t, fakeagent.Claude)
			cli := w.Client()
			setSetting(t, w.App(), "crew_memory_enabled", "true")
			body := fmt.Sprintf("{\"at\":\"2026-10-25T20:00:00Z\",\"text\":\"Remember the decision.\","+tc.reference+"}\n", crewHomeLetters["trellis"])
			writeCrewHomeFile(t, w, "trellis", filepath.Join("memory", "log.jsonl"), body)
			awake := wakeCrew(t, cli, "trellis", "")
			w.Launched(string(awake.SessionID))
			for _, forgotten := range []bool{false, true} {
				if forgotten {
					if _, err := cli.MemoryForget(awake.SessionID, 0); err != nil {
						t.Fatal(err)
					}
				}
				read, err := cli.MemoryZoom(awake.SessionID, "", "", "0")
				letter := filepath.Join(crewHome(w, "trellis"), "handoffs", crewHomeLetters["trellis"])
				if err != nil || protocol.Deref(read.Letter) != letter || protocol.Deref(read.SessionID) != "previous-session" || !strings.Contains(read.Lines[0], "[session: ") {
					t.Fatalf("session references after forgotten=%t: %+v, %v", forgotten, read, err)
				}
			}
		})
	}
}

func TestPendingMemoryTurnoverKeepsChiefAndRetirementRules(t *testing.T) {
	for _, chief := range []bool{false, true} {
		t.Run(fmt.Sprintf("chief=%v", chief), func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				setSetting(t, app, "crew_memory_enabled", "true")
				var session protocol.SessionID
				if chief {
					session = protocol.SessionID(configureChiefOn(t, w, app, fakeagent.Claude, "sonnet"))
				} else {
					if _, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"}); err != nil {
						t.Fatal(err)
					}
					session = wakeCrew(t, cli, "Keel", "").SessionID
				}
				w.bootBubbleClaude(t, string(session))
				if _, err := cli.MemoryAdd(session, "Keep the reason behind the decision."); err != nil {
					t.Fatal(err)
				}
				close := protocol.CrewSessionCloseNap
				if chief {
					close = ""
				}
				filed, err := cli.CrewHandoff(session, "Keep this decision for the next session.", false, close, "Recorded the decision and its reason.")
				if err != nil || protocol.Deref(filed.PendingSummaries) != 1 {
					t.Fatalf("pending letter = %+v %v", filed, err)
				}
				if chief {
					w.advance(time.Hour)
				} else if _, err := cli.CrewRetire("Keel"); err != nil {
					t.Fatal(err)
				}
				done, err := cli.MemorySummarize(session, []protocol.CrewMemoryAnswer{{Range: "0-1", Text: "The decision and its reason are durable."}})
				want := protocol.CrewSessionCloseSleep
				if chief {
					want = protocol.CrewSessionCloseNap
				}
				if err != nil || done.Handoff == nil || protocol.Deref(done.Handoff.Outcome) != want {
					t.Fatalf("turnover after summaries = %+v %v, want %s", done, err, want)
				}
			}, fakeagent.Claude)
		})
	}
}

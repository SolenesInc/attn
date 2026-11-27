package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/testworld"
)

const openAnswer = `{"verdict":"OPEN","label":"unread report","reason":"the fix landed and only this session reports it"}`

func supervisorActsWorld(t *testing.T, answer func(*fakeagent.HeadlessTask)) (*world, *testworld.Peer, chan string) {
	t.Helper()
	w, app, judged := supervisorWorld(t, answer)
	setSetting(t, app, "attention_supervisor", "true")
	return w, app, judged
}

func stopsQuietlyAndWaitsForItsVerdict(t *testing.T, w *world, app *testworld.Peer) (string, protocol.Session) {
	t.Helper()
	session, agent, settled := settledAgent(t, w, app)
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
	stopped := runEnds(w, app, agent, working, "The review found one bug, so I fixed it and landed it.")
	if protocol.Deref(stopped.TurnOwed) {
		t.Fatalf("the quiet stop opened a turn: %+v", stopped)
	}
	w.awaitTimer(daemon.AttentionStopTimer)
	w.advance(attentionStopWait)
	return session, stopped
}

func surfacedTurns(t *testing.T, w *world, session string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(w.Dir, attention.DecisionLogFile))
	if err != nil {
		t.Fatal(err)
	}
	surfaced := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry attention.LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decision log line %q: %v", line, err)
		}
		if string(entry.Session) == session && entry.Kind == attention.LogSurfaced {
			surfaced++
		}
	}
	return surfaced
}

func explainedVerdict(t *testing.T, w *world, session string) string {
	t.Helper()
	explained, err := w.Client().StateExplain(session)
	if err != nil {
		t.Fatalf("state explain %s: %v", session, err)
	}
	if explained.Verdict == nil {
		return ""
	}
	return attentionLine(*explained.Verdict)
}

func attentionLine(a protocol.SessionAttention) string {
	return a.Verdict.(string) + " (" + protocol.Deref(a.Label) + ") by " + a.Decider + ": " + a.Reason
}

const openLine = "open (unread report) by headless/claude-haiku-fake: the fix landed and only this session reports it"

func TestAnOpenVerdictOnAQuietStopOpensATurnThatCarriesItsReason(t *testing.T) {
	w, app, _ := supervisorActsWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(openAnswer) })
	session, _ := stopsQuietlyAndWaitsForItsVerdict(t, w, app)
	owed := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Attention != nil })
	if got := attentionLine(*owed.Attention); got != openLine {
		t.Errorf("the opened turn carries %q, want %q", got, openLine)
	}
	if got := explainedVerdict(t, w, session); got != openLine {
		t.Errorf("state explain prints the verdict %q, want %q", got, openLine)
	}

	settled := settleFromApp(app, owed)
	if settled.Attention != nil {
		t.Errorf("the settled turn still carries the verdict: %+v", settled.Attention)
	}
	if got := explainedVerdict(t, w, session); got != openLine {
		t.Errorf("after the settle state explain prints %q, want the verdict still", got)
	}
}

func TestAQuietVerdictOpensNothing(t *testing.T) {
	w, app, judged := supervisorActsWorld(t, func(task *fakeagent.HeadlessTask) {
		task.Answer(`{"verdict":"QUIET","reason":"the reviewer will report back"}`)
	})
	session, _ := stopsQuietlyAndWaitsForItsVerdict(t, w, app)
	if n := judgedCount(judged); n != 1 {
		t.Fatalf("the supervisor judged the stop %d times, want 1", n)
	}
	if after := queriedSession(t, w.Client(), session); protocol.Deref(after.TurnOwed) || after.Attention != nil {
		t.Errorf("a quiet verdict opened a turn: %+v", after)
	}
	if got, want := explainedVerdict(t, w, session), "quiet () by headless/claude-haiku-fake: the reviewer will report back"; got != want {
		t.Errorf("state explain prints the verdict %q, want %q", got, want)
	}
}

func TestARestartOpensNoTurnTheSupervisorAlreadyOpened(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		var judged atomic.Int32
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
			switch {
			case strings.Contains(task.Prompt, supervisorQuestion):
				judged.Add(1)
				task.Answer(openAnswer)
			case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
				task.Answer(`{"verdict":"DONE"}`)
			default:
				task.Answer("Refund flow")
			}
		})
		app, cli := w.TrustedApp(), w.Client()
		setSetting(t, app, "queue_mode_enabled", "true")
		setSetting(t, app, "attention_supervisor", "true")
		registerSessions(t, w, cli, "peer")
		claude := w.bubbleClaude(t, app, "shop")
		if booted := queriedSession(t, cli, claude.id); protocol.Deref(booted.TurnOwed) {
			settleFromApp(app, booted)
		}
		sendAgentMessage(t, cli, "peer", claude.id, "the review is in")
		w.advance(sessionmonitor.QuietWindow)
		pasted := claude.term.Pasted()
		if len(pasted) != 1 {
			t.Fatalf("attn pasted %q, want one doorbell", pasted)
		}
		claude.take(pasted[0])
		readInbox(t, cli, claude.id, 0)
		claude.reply("The review found one bug, so I fixed it and landed it. <!-- attn:state=idle -->")
		if stopped := queriedSession(t, cli, claude.id); protocol.Deref(stopped.TurnOwed) {
			t.Fatalf("the doorbell's run opened a turn: %+v", stopped)
		}
		w.advance(attentionStopWait)
		testworld.AwaitSession(app, claude.id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Attention != nil })

		w.restart()
		claude.cli, app = w.Client(), w.TrustedApp()
		owed := testworld.AwaitSession(app, claude.id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Attention != nil })
		if got := attentionLine(*owed.Attention); got != openLine {
			t.Errorf("after a restart the surfaced turn carries %q, want %q", got, openLine)
		}

		settleFromApp(app, owed)
		w.restart()
		claude.cli = w.Client()
		w.advance(attentionStopWait)
		if after := queriedSession(t, claude.cli, claude.id); protocol.Deref(after.TurnOwed) || after.Attention != nil {
			t.Errorf("a restart reopened the turn the user settled: %+v", after)
		}
		if n := judged.Load(); n != 1 {
			t.Errorf("the supervisor judged the stop %d times across the restarts, want 1", n)
		}
		if n := surfacedTurns(t, w, claude.id); n != 1 {
			t.Errorf("the decision log holds %d surfaced turns, want 1", n)
		}
	})
}

func TestATurnOwedByTheTimeTheVerdictArrivesRefusesTheSurface(t *testing.T) {
	var opensFirst func()
	w, app, _ := supervisorActsWorld(t, func(task *fakeagent.HeadlessTask) {
		opensFirst()
		task.Answer(openAnswer)
	})
	opensFirst = func() {}
	session, agent, settled := settledAgent(t, w, app)
	opensFirst = func() {
		if outcome, err := w.Client().AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen); err != nil || outcome.Outcome != protocol.AgentTurnOutcomeOpened {
			t.Errorf("attn turn open while the supervisor judged: %+v, %v", outcome, err)
		}
	}
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
	runEnds(w, app, agent, working, "The review found one bug, so I fixed it and landed it.")
	w.awaitTimer(daemon.AttentionStopTimer)
	w.advance(attentionStopWait)

	owed := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	if owed.Attention != nil {
		t.Errorf("the agent's own turn carries the supervisor's verdict: %+v", owed.Attention)
	}
	if n := surfacedTurns(t, w, session); n != 0 {
		t.Errorf("the decision log holds %d surfaced turns, want the surface refused", n)
	}
}

func TestWithTheSupervisorOffAnOpenVerdictOpensNothing(t *testing.T) {
	w, app, _ := supervisorWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(openAnswer) })
	session, _ := stopsQuietlyAndWaitsForItsVerdict(t, w, app)
	if got := explainedVerdict(t, w, session); got != openLine {
		t.Fatalf("state explain prints the verdict %q, want %q", got, openLine)
	}
	if after := queriedSession(t, w.Client(), session); protocol.Deref(after.TurnOwed) || after.Attention != nil {
		t.Errorf("an open verdict with the supervisor off opened a turn: %+v", after)
	}
	if n := surfacedTurns(t, w, session); n != 0 {
		t.Errorf("the decision log holds %d surfaced turns with the supervisor off", n)
	}
}

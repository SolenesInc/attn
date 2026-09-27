package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAStopWithOpenTodosWaitsForTheUserWithoutAskingTheModel(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	transcript, _ := hookedClaudeAtWork(t, w, app, cli, "migrate the orders table")
	if err := cli.UpdateTodos("s1", []string{"[✓] write the migration", "[ ] run it against staging"}); err != nil {
		t.Fatalf("report todos: %v", err)
	}
	transcript.Answer("The migration is written; staging is next.")
	if err := cli.SendStop("s1", transcript.Path, client.StopFacts{}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestAStopWithEveryTodoDoneTakesTheModelsVerdict(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	transcript, _ := hookedClaudeAtWork(t, w, app, cli, "migrate the orders table")
	if err := cli.UpdateTodos("s1", []string{"[✓] write the migration", "[✓] run it against staging"}); err != nil {
		t.Fatalf("report todos: %v", err)
	}
	transcript.Answer("Staging is migrated. Should I run it against production?")
	if err := cli.SendStop("s1", transcript.Path, client.StopFacts{}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	answerTurnVerdict(t, w, "WAITING")
	testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestATurnIsJudgedOnceHoweverManyStopsReportIt(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	transcript, _ := hookedClaudeAtWork(t, w, app, cli, "migrate the orders table")
	transcript.Answer("The migration is written. Should I run it?")
	stop := func() {
		t.Helper()
		if err := cli.SendStop("s1", transcript.Path, client.StopFacts{}); err != nil {
			t.Fatalf("stop: %v", err)
		}
	}
	stop()
	verdict := w.HeadlessTask()
	stop()
	verdict.Answer(`{"verdict":"WAITING"}`)
	testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	stop()

	transcript.Prompt("yes, run it")
	if err := cli.UpdateStateFromHookEvidence("s1", protocol.StateWorking, "", "user_prompt_submit", "yes, run it"); err != nil {
		t.Fatalf("report the prompt taken: %v", err)
	}
	transcript.Answer("Ran it against staging.")
	stop()
	next := w.HeadlessTask()
	if !strings.Contains(next.Prompt, "Ran it against staging.") {
		t.Fatalf("the next model call judged %q, want the new turn: an old turn was judged twice", next.Prompt)
	}
	next.Answer(`{"verdict":"DONE"}`)
	testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

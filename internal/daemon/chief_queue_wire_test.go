package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func chiefMemberInQueue(t *testing.T, settings ...string) (*testworld.Peer, *client.Client, *fakeagent.Run, string, protocol.Session) {
	t.Helper()
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "queue_mode_enabled", "true")
	for _, key := range settings {
		setSetting(t, app, key, "true")
	}
	profileChief(t, w)
	registerSessions(t, w, cli, "reviewer")
	run := wakeConfiguredChief(t, w, cli)
	session := string(chiefSession(t, cli))
	greeted := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.TurnOwed)
	})
	return app, cli, run, session, greeted
}

func TestTheChiefMemberOwesATurnTheWayAnyCrewMemberDoes(t *testing.T) {
	app, cli, run, session, greeted := chiefMemberInQueue(t)
	working, batch := notificationRunStarts(t, app, cli, run, session, greeted, "the delegate reported")
	if !protocol.Deref(batch.QuietNotifications) {
		t.Errorf("reading its inbox in a notification run, the Chief was not told it stays out of the queue: %+v", batch)
	}
	working = notificationRunSettled(app, working)
	run.Reply("Noted in the journal. <!-- attn:state=idle -->")
	quiet := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if protocol.Deref(quiet.TurnOwed) {
		t.Fatal("the stop of a run a ring started opened a turn with quiet notifications on")
	}

	working, _ = notificationRunStarts(t, app, cli, run, session, quiet, "the delegate finished the review")
	opened, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen)
	if err != nil || opened.Outcome != protocol.AgentTurnOutcomeOpensAtStop {
		t.Fatalf("attn turn open while working = %+v, %v; want it to open at the stop", opened, err)
	}
	run.Reply("The review is ready for you. <!-- attn:state=idle -->")
	asked := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(asked.TurnOwed) || !protocol.Deref(asked.TurnByAgent) {
		t.Fatalf("after attn turn open the Chief's stop shows owed=%v by_agent=%v, want its own turn open", protocol.Deref(asked.TurnOwed), protocol.Deref(asked.TurnByAgent))
	}

	settled := settleFromApp(app, asked)
	stopped := userRunStops(app, run, settled, "what is open?", "Two seeds. <!-- attn:state=waiting_input -->")
	if !protocol.Deref(stopped.TurnOwed) || protocol.Deref(stopped.TurnByAgent) {
		t.Fatalf("a run the user started stopped with owed=%v by_agent=%v, want the user's turn open", protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnByAgent))
	}
	if !protocol.Deref(stopped.ChiefOfStaff) || protocol.Deref(stopped.CrewMember) == "" {
		t.Fatalf("the Chief member shows chief=%v member=%q, want both", protocol.Deref(stopped.ChiefOfStaff), protocol.Deref(stopped.CrewMember))
	}
}

func TestAnsweringTheChiefsAskClosesItsTurnAndTheAnswersRunOpensNone(t *testing.T) {
	app, cli, run, session, greeted := chiefMemberInQueue(t, "asks_enabled")
	if guidance, _ := flagValue(run.Argv, "--append-system-prompt"); !strings.Contains(guidance, askGuidanceLead) {
		t.Fatalf("a Chief launched with asks on was not taught them:\n%s", guidance)
	}
	asked, err := cli.AskOpen(protocol.SessionID(session), "", cacheAsk, "", "")
	if err != nil {
		t.Fatalf("the Chief's ask: %v", err)
	}

	answered := testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: "chief-answer", AskID: asked.ID,
		Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}},
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == "chief-answer" })
	if !answered.Success {
		t.Fatalf("answering failed: %s", protocol.Deref(answered.Error))
	}
	if got := run.Prompted(); !strings.Contains(got, "Postgres") {
		t.Fatalf("the Chief took %q, want the answer", got)
	}
	working := testworld.AwaitStateAfter(app, greeted, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && s.TurnOwed == nil && protocol.Deref(s.TurnByAgent)
	})
	run.Reply("Postgres it is. <!-- attn:state=waiting_input -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	if protocol.Deref(stopped.TurnOwed) {
		t.Fatal("the run the answer started opened a turn with quiet notifications on")
	}
}

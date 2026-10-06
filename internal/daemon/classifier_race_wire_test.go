package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAVerdictLandingAfterAnApprovalLeavesTheApprovalStanding(t *testing.T) {
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		transcript, _ := hookedClaudeAtWork(t, w, app, cli, "migrate the orders table")
		transcript.Answer("The migration is written.")
		if err := cli.SendStop(protocol.TerminalID(w.Terminal("s1")), transcript.Path, client.StopFacts{}); err != nil {
			t.Fatalf("stop: %v", err)
		}
		verdict := w.HeadlessTask()

		if err := cli.RecordNotification(protocol.TerminalID(w.Terminal("s1")), "permission_prompt", "Allow the migration to run?"); err != nil {
			t.Fatalf("notify: %v", err)
		}
		asking := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
		before := len(sessionUpdatesOf(app, "s1"))
		verdict.Answer(`{"verdict":"DONE"}`)
		w.advance(0)

		if updates := sessionUpdatesOf(app, "s1")[before:]; len(updates) != 0 {
			t.Fatalf("the verdict about the turn before the approval moved the session: %s", describeUpdates(updates))
		}
		if got := queriedSession(t, cli, "s1"); got.State != protocol.SessionStatePendingApproval || got.StateSince != asking.StateSince {
			t.Fatalf("after the late verdict the session is %s since %s, want the approval since %s", got.State, got.StateSince, asking.StateSince)
		}
	})
}

func TestAnAutoSettleCountdownStandsDownWhileTheTurnsStopIsJudgedAndRearmsAfter(t *testing.T) {
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "auto_settle_enabled", "true")
		transcript, _ := hookedClaudeAtWork(t, w, app, cli, "look around")
		if err := cli.UpdateState(protocol.TerminalID(w.Terminal("s1")), protocol.StateWaitingInput); err != nil {
			t.Fatalf("report waiting_input: %v", err)
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
		transcript.Prompt("deploy to staging")
		steered := autoSettleSteer(t, w, app, cli, "deploy to staging")
		w.advance(autoSettleDefaultArm)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return autoSettleFiresAt(t, s).Equal(steered.Add(autoSettleDefaultArm + autoSettleDefaultCountdown))
		})

		transcript.Answer("The deploy is running in the background; I'll continue when it completes.")
		yieldWithBackgroundWork(t, w, cli, transcript)
		verdict := w.HeadlessTask()
		judging := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.StateReason) == "background_work" })
		if judging.State != protocol.SessionStateWorking || !protocol.Deref(judging.TurnOwed) || judging.AutoSettleFiresAt != nil {
			t.Errorf("while the stop is judged the session is %s owed=%v fires_at=%q, want working with the turn owed and no countdown",
				judging.State, protocol.Deref(judging.TurnOwed), protocol.Deref(judging.AutoSettleFiresAt))
		}

		verdict.Answer(`{"verdict":"PARKED"}`)
		w.advance(0)
		judged := time.Now()
		if shown := sessionStateLastShown(t, app); shown.State != protocol.SessionStateWorking || !protocol.Deref(shown.TurnOwed) || shown.AutoSettleFiresAt != nil {
			t.Fatalf("after the parked verdict the app shows %s owed=%v fires_at=%q, want working, owed and arming afresh",
				shown.State, protocol.Deref(shown.TurnOwed), protocol.Deref(shown.AutoSettleFiresAt))
		}
		w.advance(autoSettleDefaultArm)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return autoSettleFiresAt(t, s).Equal(judged.Add(autoSettleDefaultArm + autoSettleDefaultCountdown))
		})
	})
}

package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func sigmaInARun(t *testing.T, w *world) (*testworld.Peer, string, string, *fakeagent.AppServer) {
	t.Helper()
	app := w.App()
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	promptUntilWorking(app, session, view, server, "fix the flaky checkout test")
	return app, session, view.ConversationID, server
}

func TestFeedbackReachesAnIdleSigmaConversationAndSteersARunningOne(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app, session, conversation, server := sigmaInARun(t, w)

	if r := submitSessionAnnotationFeedbackAs(app, session, "mid-run", "also cover refunds"); !r.Success || r.Status != "delivered" {
		t.Fatalf("feedback during the run = %+v, want it delivered", r)
	}
	if got := server.Prompted(conversation); got.Text != "also cover refunds" || !got.Steer {
		t.Fatalf("Sigma took %+v during the run, want the feedback as a steer of that run", got)
	}
	if got := queriedSession(t, w.Client(), session).State; got != protocol.SessionStateWorking {
		t.Fatalf("after the steer the session is %s, want it still working on its run", got)
	}
	server.Reply(conversation, "Fixed, refunds too. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	mark := len(app.Log())
	if r := submitSessionAnnotationFeedbackAs(app, session, "idle", "now the coupons"); !r.Success || r.Status != "delivered" {
		t.Fatalf("feedback at idle = %+v, want it delivered", r)
	}
	if got := server.Prompted(conversation); got.Text != "now the coupons" {
		t.Fatalf("Sigma took %+v at idle, want the feedback", got)
	}
	testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
}

func TestFeedbackDuringASigmaApprovalWaitsInSigmaUntilTheApprovalIsAnswered(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app, session, conversation, server := sigmaInARun(t, w)
	server.AskApproval(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })

	if r := submitSessionAnnotationFeedbackAs(app, session, "during-approval", "check the rounding"); !r.Success || r.Status != "delivered" {
		t.Fatalf("feedback during the approval = %+v, want it handed to Sigma", r)
	}
	if got := server.Submissions(conversation); got != 2 {
		t.Fatalf("Sigma admitted %d inputs during the approval, want the prompt and the feedback", got)
	}
	if got := queriedSession(t, w.Client(), session).State; got != protocol.SessionStatePendingApproval {
		t.Fatalf("the feedback left the session %s, want the approval still open", got)
	}

	server.Work(conversation)
	if got := server.Prompted(conversation); got.Text != "check the rounding" || !got.Steer {
		t.Fatalf("after the approval Sigma's run took %+v, want the feedback as a steer", got)
	}
	server.Reply(conversation, "Rounded. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestASigmaSessionThatClosesCompactingCompactsOnceItsRunEndsAndThenCloses(t *testing.T) {
	for _, tc := range []struct {
		answer      fakeagent.ServerCompaction
		compactions int
		failure     string
	}{
		{answer: fakeagent.ServerCompaction{Outcome: "compacted"}, compactions: 1},
		{answer: fakeagent.ServerCompaction{Outcome: "nothing_to_compact"}},
		{answer: fakeagent.ServerCompaction{Outcome: "failed", Message: "context window unavailable"}, failure: "context window unavailable"},
		{answer: fakeagent.ServerCompaction{Outcome: "stopped"}, failure: "stopped the compaction"},
	} {
		t.Run(tc.answer.Outcome, func(t *testing.T) {
			w := newWorld(t, fakeagent.Sigma)
			app, session, conversation, server := sigmaInARun(t, w)
			cli := w.Client()
			server.AnswerNextCompaction(conversation, tc.answer)

			closeCompacting(t, cli, session)
			if got := server.Compactions(conversation); got != 0 {
				t.Fatalf("the conversation compacted %d times during the run that asked to close", got)
			}
			stillOpen(t, cli, session, "during the run that asked to close")

			server.Reply(conversation, "Fixed and closing. <!-- attn:state=idle -->")
			closed := awaitClosed(app, session)
			reason := protocol.Deref(closed.CloseReason)
			if protocol.Deref(closed.ClosedBy) != session || !strings.HasPrefix(reason, compactReason) {
				t.Fatalf("ledger entry = %+v, want it closed by itself for %q", closed, compactReason)
			}
			if tc.failure == "" && reason != compactReason {
				t.Errorf("the session closed for %q after Sigma answered %s, want %q alone", reason, tc.answer.Outcome, compactReason)
			}
			if tc.failure != "" && (!strings.Contains(reason, "the compact did not run") || !strings.Contains(reason, tc.failure)) {
				t.Errorf("the session closed for %q after Sigma answered %s, want it to say the compact did not run: %s", reason, tc.answer.Outcome, tc.failure)
			}
			if got := server.Compactions(conversation); got != tc.compactions {
				t.Errorf("the conversation compacted %d times before the close, want %d", got, tc.compactions)
			}
			server.AwaitInterrupted(conversation)
		})
	}
}

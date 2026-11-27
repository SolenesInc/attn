package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/testworld"
)

func sigmaInARun(t *testing.T, w *world) (*testworld.Peer, string, string, *fakeagent.AppServer) {
	t.Helper()
	app := w.App()
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	promptUntilWorking(app, session, view, server, "fix the flaky checkout test")
	return app, session, view.ConversationID, server
}

func TestFeedbackDuringASigmaRunSteersThatRun(t *testing.T) {
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
}

func TestFeedbackDuringASigmaApprovalReachesSigmaAsASteerAndLeavesTheApprovalOpen(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app, session, conversation, server := sigmaInARun(t, w)
	server.AskApproval(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })

	if r := submitSessionAnnotationFeedbackAs(app, session, "during-approval", "check the rounding"); !r.Success || r.Status != "delivered" {
		t.Fatalf("feedback during the approval = %+v, want it handed to Sigma", r)
	}
	if got := server.Prompted(conversation); got.Text != "check the rounding" || !got.Steer {
		t.Fatalf("Sigma took %+v during the approval, want the feedback as a steer, which waits in Sigma's inbox until the run's next step", got)
	}
	if got := queriedSession(t, w.Client(), session).State; got != protocol.SessionStatePendingApproval {
		t.Fatalf("the feedback left the session %s, want the approval still open", got)
	}

	server.Work(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Rounded. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestFeedbackDuringAWorkersApprovalReachesTheRootWhoseRunEnded(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app, session, conversation, server := sigmaInARun(t, w)
	server.AskApproval(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	server.ReplyWhileAWorkerWaits(conversation, "Asked a worker to check the tax table.")

	if r := submitSessionAnnotationFeedbackAs(app, session, "worker-approval", "check the rounding too"); !r.Success || r.Status != "delivered" {
		t.Fatalf("feedback while only a worker waits on approval = %+v, want it delivered to the root at once", r)
	}
	if got := server.Prompted(conversation); got.Text != "check the rounding too" || !got.Steer {
		t.Fatalf("Sigma took %+v, want the feedback submitted to the root", got)
	}
	if got := queriedSession(t, w.Client(), session).State; got != protocol.SessionStatePendingApproval {
		t.Fatalf("the feedback left the session %s, want the worker's approval still open", got)
	}
}

func TestFeedbackDuringASigmaApprovalReachesSigmaAsASteerAfterADaemonRestart(t *testing.T) {
	t.Setenv("ATTN_PTY_HOST_BINARY", testworld.SharedHostBinary(t))
	w := newWorld(t, fakeagent.Sigma)
	t.Cleanup(func() { ptyhost.ReapDataDir(w.Dir) })
	app, session, conversation, server := sigmaInARun(t, w)
	server.AskApproval(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })

	w.restart()
	app = w.App()
	testworld.AwaitSessionFromConnect(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	if r := submitSessionAnnotationFeedbackAs(app, session, "after-restart", "check the rounding"); !r.Success || r.Status != "delivered" {
		t.Fatalf("feedback during the approval after a daemon restart = %+v, want it handed to Sigma as before the restart", r)
	}
	if got := server.Prompted(conversation); got.Text != "check the rounding" || !got.Steer {
		t.Fatalf("Sigma took %+v after the restart, want the feedback as a steer that waits for the approval", got)
	}
	if got := queriedSession(t, w.Client(), session).State; got != protocol.SessionStatePendingApproval {
		t.Fatalf("the feedback left the session %s, want the approval still open", got)
	}
}

func TestDocumentAnnotationsAndReviewQuestionsDuringASigmaApprovalReachSigmaAsSteers(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app, session, conversation, server := sigmaInARun(t, w)
	setSetting(t, app, "code_review_enabled", "true")
	doc := fileAnnotatedDocument("/notes/plan.md")
	global := protocol.MarkdownAnnotation{ID: "g1", Type: "global", Text: protocol.Ptr("tighten the intro"), CreatedAt: 1}
	question := protocol.MarkdownAnnotation{ID: "q1", Type: "question", Text: protocol.Ptr("Why this order?"), CreatedAt: 2,
		Anchor: &protocol.MarkdownAnnotationAnchor{StartLine: 1, EndLine: 1, Exact: "Plan"}}
	saveDocumentAnnotations(app, doc, []protocol.MarkdownAnnotation{global, question})
	server.AskApproval(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })

	asked := submitReview(app, doc, session, reviewSend{onlyIDs: []string{"q1"}})
	if !asked.Success || asked.Status != "delivered" || len(asked.Annotations) != 2 {
		t.Fatalf("a review question during the approval = %+v, want it handed to Sigma", asked)
	}
	if got := server.Prompted(conversation); !got.Steer || !strings.Contains(got.Text, "Why this order?") {
		t.Fatalf("Sigma took %+v for the question, want it as a steer", got)
	}
	sent := submitDocumentAnnotations(app, doc, session, "", nil)
	if !sent.success || sent.status != "delivered" {
		t.Fatalf("document annotations during the approval = %+v, want them handed to Sigma", sent)
	}
	if got := server.Prompted(conversation); !got.Steer || !strings.Contains(got.Text, "tighten the intro") {
		t.Fatalf("Sigma took %+v for the annotations, want them as a steer", got)
	}
	if draft := getDocumentAnnotations(app, doc); len(draft.Annotations) != 0 {
		t.Fatalf("the draft after the batch was placed = %+v, want it spent", draft.Annotations)
	}
	if got := queriedSession(t, w.Client(), session).State; got != protocol.SessionStatePendingApproval {
		t.Fatalf("the sends left the session %s, want the approval still open", got)
	}
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
			stillOpen(t, cli, session, "during the run that asked to close")

			server.Reply(conversation, "Fixed and closing. <!-- attn:state=idle -->")
			closed := awaitClosed(app, session)
			if protocol.Deref(closed.ClosedBy) != session {
				t.Fatalf("ledger entry = %+v, want it closed by itself", closed)
			}
			if tc.failure != "" {
				compactDidNotRun(t, closed, tc.failure)
			} else if reason := protocol.Deref(closed.CloseReason); reason != compactReason {
				t.Errorf("the session closed for %q after Sigma answered %s, want %q alone", reason, tc.answer.Outcome, compactReason)
			}
			compacted, duringARun := server.Compactions(conversation)
			if compacted != tc.compactions || duringARun != 0 {
				t.Errorf("the conversation compacted %d times, %d asked during a run, want %d and none during a run", compacted, duringARun, tc.compactions)
			}
			server.AwaitInterrupted(conversation)
		})
	}
}

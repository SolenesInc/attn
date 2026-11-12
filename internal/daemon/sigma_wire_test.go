package daemon_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

func submitSessionAnnotationFeedbackAs(app *testworld.Peer, sessionID, requestID, text string) protocol.SessionAnnotationsSubmitResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{
		Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: requestID, SessionID: protocol.SessionID(sessionID), Text: text,
	}, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == requestID })
}

func TestASigmaViewAttachesToTheServerAttnRuns(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	_, view, _ := appServerSession(t, w, w.App(), fakeagent.Sigma)
	if !slices.Contains(view.Argv, "--attach") {
		t.Errorf("the Sigma view ran %q, want --attach so it never starts a second server", view.Argv)
	}
}

func TestASigmaQuestionAttnNeverSawStartReachesTheUserAndARetryDoesNotRunItAgain(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	conversation := view.ConversationID
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	server.LoseNextSubmit(conversation)
	delivery := uuid.NewString()
	if lost := submitSessionAnnotationFeedbackAs(app, session, delivery, sessionAnnotationFeedback); lost.Success && lost.Status == "delivered" {
		t.Fatalf("feedback whose reply the server lost = %+v, want it not reported delivered", lost)
	}
	server.Prompted(conversation)
	server.Reply(conversation, "By date or by customer? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed)
	})

	if retried := submitSessionAnnotationFeedbackAs(app, session, delivery, sessionAnnotationFeedback); !retried.Success || retried.Status != "delivered" {
		t.Fatalf("retrying the same feedback = %+v, want it delivered", retried)
	}
	if admitted := server.Submissions(conversation); admitted != 1 {
		t.Fatalf("the conversation admitted %d inputs, want the retry to find the first", admitted)
	}
	if got := queriedSession(t, w.Client(), session); got.State != protocol.SessionStateWaitingInput {
		t.Errorf("after the retry the session is %s, want it still waiting on its question", got.State)
	}
}

func TestASigmaQuestionTheUserHandledStaysHandledThroughReconnectsAndRestarts(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
	w := newWorld(t, fakeagent.Sigma)
	t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
	app := w.App()
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	neighbour := w.Spawn(app, fakeagent.Sigma, w.Path("neighbour"))
	neighbourView := w.Launched(neighbour)
	conversation := view.ConversationID
	promptUntilWorking(app, session, view, server, "which index should the report use?")
	server.Reply(conversation, "By date or by customer? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed)
	})
	app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: protocol.SessionID(session)})
	handled := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return !protocol.Deref(s.TurnOwed) })

	stillHandled := func(when string) {
		t.Helper()
		mark := len(app.Log())
		app.TypeLine(neighbour, "keep the server connected")
		neighbourView.Prompted()
		server.Prompted(neighbourView.ConversationID)
		testworld.AwaitSessionSince(app, mark, neighbour, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		server.Reply(neighbourView.ConversationID, "Connected. <!-- attn:state=idle -->")
		testworld.AwaitSessionSince(app, mark, neighbour, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		got := queriedSession(t, w.Client(), session)
		if protocol.Deref(got.TurnOwed) || protocol.Deref(got.TurnOpenedAt) != protocol.Deref(handled.TurnOpenedAt) {
			t.Fatalf("%s the session is %s with turn owed %v, opened at %s; want the turn opened at %s and handled to stay handled", when, got.State, protocol.Deref(got.TurnOwed), protocol.Deref(got.TurnOpenedAt), protocol.Deref(handled.TurnOpenedAt))
		}
	}

	server.LoseNextSubmit(neighbourView.ConversationID)
	if lost := submitSessionAnnotationFeedbackAs(app, neighbour, uuid.NewString(), sessionAnnotationFeedback); lost.Success && lost.Status == "delivered" {
		t.Fatalf("feedback whose reply the server lost = %+v", lost)
	}
	server.Prompted(neighbourView.ConversationID)
	server.Reply(neighbourView.ConversationID, "Noted. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, neighbour, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	stillHandled("after attn reconnected to the server")

	w.restart()
	app = w.App()
	testworld.AwaitSessionFromConnect(app, neighbour, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	stillHandled("after a daemon restart")
}

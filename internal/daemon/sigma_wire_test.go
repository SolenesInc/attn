package daemon_test

import (
	"slices"
	"strconv"
	"strings"
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

func TestClosingASigmaSessionLeavesItsRunGoingForAReopen(t *testing.T) {
	closes := map[string]func(app *testworld.Peer, session string, view *fakeagent.Run){
		"the view exits":     func(_ *testworld.Peer, _ string, view *fakeagent.Run) { view.Exit(0) },
		"the user closes it": func(app *testworld.Peer, session string, _ *fakeagent.Run) { closeSessionPane(app, session) },
	}
	for name, closeSession := range closes {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Sigma)
			app := w.App()
			session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
			conversation := view.ConversationID
			promptUntilWorking(app, session, view, server, "rename the orders table")

			closeSession(app, session, view)
			testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool { return e.Session != nil && string(e.Session.ID) == session })
			if entry := ledgerShowOverTheWebSocket(app, session).Entry; entry == nil || protocol.Deref(entry.ClosedAt) == "" {
				t.Fatalf("the ledger shows %+v, want %s closed", entry, session)
			}
			if reopened := reopenOverTheWebSocket(app, session); !reopened.Success {
				t.Fatalf("reopen: %s", protocol.Deref(reopened.Error))
			}
			w.Launched(session)
			if status := server.Started(conversation).Status; status != "working" {
				t.Fatalf("after the session closed and reopened the conversation is %s, want its run still working", status)
			}
			testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			server.Reply(conversation, "Renamed. <!-- attn:state=idle -->")
			testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		})
	}
}

func TestSigmaListsTheExecutionTargetsOfAFolderAndNisseHasNone(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	setSetting(t, app, "sigma_enabled", "true")
	folder := w.Path("shop")
	listed := testworld.Request(app, protocol.HarnessTargetsMessage{Cmd: protocol.CmdHarnessTargets, RequestID: "targets", Harness: "sigma", Cwd: folder},
		protocol.EventHarnessTargetsResult, func(r protocol.HarnessTargetsResultMessage) bool { return r.RequestID == "targets" })
	var want []protocol.ExecutionTarget
	for _, target := range fakeagent.ServerTargets(folder) {
		want = append(want, protocol.ExecutionTarget{ID: target.ID, Name: target.Name, Available: target.Available, Detail: target.Detail})
	}
	if !listed.Success || !slices.Equal(listed.Targets, want) {
		t.Fatalf("Sigma's targets for %s = %+v, want %+v", folder, listed, want)
	}

	setSetting(t, app, "nisse_enabled", "true")
	nisse := testworld.Request(app, protocol.HarnessTargetsMessage{Cmd: protocol.CmdHarnessTargets, RequestID: "nisse-targets", Harness: "nisse", Cwd: folder},
		protocol.EventHarnessTargetsResult, func(r protocol.HarnessTargetsResultMessage) bool { return r.RequestID == "nisse-targets" })
	if nisse.Success {
		t.Errorf("Nisse's targets = %+v, want a refusal", nisse)
	}
	refused, _, _ := w.RequestSpawn(app, fakeagent.Nisse, folder, func(m *protocol.SpawnSessionMessage) { m.ExecutionTarget = protocol.Ptr("devvm") })
	if refused.Success {
		t.Errorf("a Nisse launch on target devvm succeeded, want it refused")
	}
}

func TestASigmaConversationStartsOnTheTargetItsSessionLaunchedWith(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			w := newWorld(t, fakeagent.Sigma)
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", strconv.FormatBool(direct))
			_, view, server := appServerSession(t, w, app, fakeagent.Sigma, func(m *protocol.SpawnSessionMessage) { m.ExecutionTarget = protocol.Ptr("devvm") })
			if started := server.Started(view.ConversationID); started.Target != "devvm" {
				t.Fatalf("the conversation started on %q, want devvm", started.Target)
			}
		})
	}
}

func TestASigmaSessionWhoseTargetIsUnavailableKeepsItsErrorAndTriesThatTargetAgainOnResumeAndReopen(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	setSetting(t, app, "sigma_enabled", "true")
	session := w.Spawn(app, fakeagent.Sigma, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.ExecutionTarget = protocol.Ptr("gone") })
	failed, failure := w.LaunchFailed(session)
	if target, _ := flagValue(failed.Argv, "--target"); target != "gone" || !strings.Contains(failure, "TargetUnavailable") {
		t.Fatalf("the view ran %q and failed with %q, want it opened on target gone and refused as TargetUnavailable", failed.Argv, failure)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.TerminalExit != nil })
	resumed := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
	if !resumed.Success {
		t.Fatalf("resume: %s", protocol.Deref(resumed.Error))
	}
	again, _ := w.LaunchFailed(session)
	if target, _ := flagValue(again.Argv, "--target"); target != "gone" {
		t.Fatalf("the resumed view ran %q, want it on target gone again", again.Argv)
	}

	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.TerminalExit != nil })
	if closed := closeFromApp(app, session); !closed.Accepted {
		t.Fatalf("close: %s", protocol.Deref(closed.Error))
	}
	awaitClosed(app, session)
	if reopened := reopenOverTheWebSocket(app, session); !reopened.Success {
		t.Fatalf("reopen: %s", protocol.Deref(reopened.Error))
	}
	fromLedger, _ := w.LaunchFailed(session)
	if target, _ := flagValue(fromLedger.Argv, "--target"); target != "gone" {
		t.Fatalf("the view reopened from the ledger ran %q, want it on target gone again", fromLedger.Argv)
	}
}

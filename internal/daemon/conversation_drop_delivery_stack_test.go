package daemon_test

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADroppedConversationMoveKeepsItsInFlightDeliveryUsable(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	owners := s.PauseEvery(pausepoint.ConversationOwnerResolved)
	bound := s.PauseAt(pausepoint.ConversationCandidateBound)
	s.Start()
	app := s.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	spawnWaiting := func(prompt string) (string, *fakeagent.Run) {
		t.Helper()
		session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
		agent := s.Launched(session)
		app.TypeLine(session, prompt)
		agent.Prompted()
		agent.Reply("Ready. <!-- attn:state=waiting_input -->")
		testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
		return session, agent
	}
	first, firstAgent := spawnWaiting("find the checkout race")
	second, secondAgent := spawnWaiting("review the refund flow")
	originalSecond := secondAgent.ConversationID
	app.TypeLine(first, "/new")
	firstAgent.Prompted()
	openFirst := owners.Take()
	conversation := firstAgent.ConversationID
	app.TypeLine(second, "/resume "+conversation)
	secondAgent.Prompted()
	dropSecond := owners.Take()
	openFirst()
	next := awaitSuccessor(app, first)
	bound.Await()
	bound.Release()

	server := s.CodexServer()
	halt := sync.OnceFunc(func() { server.Halt(conversation) })
	t.Cleanup(halt)
	awaitDelivery, releaseDelivery := server.HoldNextTurnReply()
	deliveryApp := s.App()
	request := protocol.SessionAnnotationsSubmitMessage{
		Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: uuid.NewString(), SessionID: protocol.SessionID(second), Text: "fix the checkout race",
	}
	deliveryApp.Send(request)
	awaitDelivery()
	mark := len(app.Log())
	dropSecond()
	testworld.AwaitSessionSince(app, mark, second, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	releaseDelivery()
	placed := feedbackResult(deliveryApp, request.RequestID)
	if placed.Status != "delivered" {
		t.Fatalf("in-flight delivery = %+v, want delivered", placed)
	}
	if got := server.Prompted(conversation); got != request.Text {
		t.Fatalf("in-flight prompt = %q, want %q", got, request.Text)
	}
	if sent := submitSessionAnnotationFeedback(deliveryApp, second, "also cover refunds"); sent.status != "delivered" {
		t.Fatalf("delivery after the dropped move = %+v, want delivered", sent)
	}
	if got := server.Prompted(conversation); got != "also cover refunds" {
		t.Fatalf("next prompt = %q, want also cover refunds", got)
	}
	if sent := submitSessionAnnotationFeedback(deliveryApp, string(next.ID), "keep the bound conversation"); sent.status != "delivered" {
		t.Fatalf("delivery to the bound owner = %+v, want delivered", sent)
	}
	if got := server.Prompted(conversation); got != "keep the bound conversation" {
		t.Fatalf("bound owner's prompt = %q, want keep the bound conversation", got)
	}
	halt()
	reloadRespawned(t, app, second)
	if resumed := s.Launched(second); resumed.ConversationID != originalSecond {
		t.Fatalf("refused session resumed %s, want %s", resumed.ConversationID, originalSecond)
	}
	reloadRespawned(t, app, string(next.ID))
	if resumed := s.Launched(string(next.ID)); resumed.ConversationID != conversation {
		t.Fatalf("bound session resumed %s, want %s", resumed.ConversationID, conversation)
	}
}

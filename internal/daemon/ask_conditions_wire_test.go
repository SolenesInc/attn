package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/testworld"
)

func answerWith(app *testworld.Peer, askID string, answer protocol.AskAnswer) protocol.AskAnswerResultMessage {
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: requestID, AskID: askID, Answers: []protocol.AskAnswer{answer},
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == requestID })
}

func TestAPickCarriesTheUsersConditionToTheAgentTheAppAndThePhone(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	setSetting(t, app, "phone_access_enabled", "true")
	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.welcomed()
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(asker)
	asked, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatal(err)
	}

	condition := protocol.Ptr("skip the Windows VM step")
	refused := answerWith(app, asked.ID, protocol.AskAnswer{Key: "q1", Picked: []string{}, Other: protocol.Ptr("SQLite"), Condition: condition})
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "takes a condition only on a pick") {
		t.Fatalf("a condition without a pick = %+v, want a refusal", refused)
	}

	if answered := answerWith(app, asked.ID, protocol.AskAnswer{Key: "q1", Picked: []string{"Postgres"}, Condition: condition}); !answered.Success {
		t.Fatalf("answering with a condition failed: %s", protocol.Deref(answered.Error))
	}
	if typed := agent.Prompted(); !strings.Contains(typed, "Answer: Postgres (the recommended choice)\n\nTheir condition: skip the Windows VM step") {
		t.Errorf("the agent was typed %q, want the condition after the answer line", typed)
	}
	replied := testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool {
		return slices.ContainsFunc(m.Asks, func(a protocol.Ask) bool { return a.ID == asked.ID && a.Reply != nil })
	})
	for _, a := range replied.Asks {
		if a.ID == asked.ID && protocol.Deref(a.Reply.Answers[0].Condition) != *condition {
			t.Errorf("the app shows the reply %+v, want the condition", a.Reply.Answers)
		}
	}
	onPhone := phone.showsAsks(func(asks []homegateway.HomeGatewayAsk) bool {
		return slices.ContainsFunc(asks, func(a homegateway.HomeGatewayAsk) bool { return a.ID == asked.ID && a.Reply != nil })
	})
	for _, a := range onPhone {
		if a.ID == asked.ID && protocol.Deref(a.Reply.Answers[0].Condition) != *condition {
			t.Errorf("the phone shows the reply %+v, want the condition", a.Reply.Answers)
		}
	}
}

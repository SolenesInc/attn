package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAHalfTypedDraftSurvivesTheTurnEndingAndAttnsMail(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	recipient, agent := mailIdleAgent(w, app, "shop")
	registerSessions(t, w, cli, "sender")
	app.TypeLine(recipient, "keep going")
	agent.Prompted()

	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: recipient, Data: "half a thought"})
	app.AwaitScreen(recipient, "half a thought")
	agent.Reply("Done for now. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	held := sendAgentMessage(t, cli, "sender", recipient, "the build is green")
	if held.Status != protocol.AgentMsgStatusQueued {
		t.Errorf("mail for an agent under a fresh draft = %+v, want it held back", held)
	}
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: recipient, Data: " about checkout\r"})
	if got := agent.Prompted(); got != "half a thought about checkout" {
		t.Fatalf("the agent received %q, want the user's draft exactly as typed", got)
	}
}

package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestMailForAnAgentShowingASelectorTypesNothingAtItAndRingsOnceItClears(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	recipient, agent := mailIdleAgent(w, app, "shop")
	sender := spawnPanes(w, app, w.Path("sender"))[0].session

	agent.ShowSelector()
	app.AwaitScreen(recipient, "Esc to cancel")
	sent := sendAgentMessage(t, cli, sender, recipient, "the rollback is ready")
	if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "waiting on a keypress") {
		t.Fatalf("a message to an agent showing a selector = %+v, want queued until the selector clears", sent)
	}
	if typed := agent.Dismiss(); typed != "" {
		t.Fatalf("attn typed %q at the agent's selector", typed)
	}

	app.TypeLine(recipient, "keep the old import path")
	if got := agent.Prompted(); got != "keep the old import path" {
		t.Fatalf("after the selector cleared the agent was prompted with %q, want the user's line first", got)
	}
	agent.Reply("Kept it. <!-- attn:state=idle -->")
	if got := agent.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("once its turn ended the agent was prompted with %q, want the inbox doorbell", got)
	}
}

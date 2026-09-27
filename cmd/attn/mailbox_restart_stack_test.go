package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const inboxDoorbell = "You have unread items in your attn inbox. Run attn agent inbox to read them."

func TestMailQueuedForABusyAgentRingsItOnceAfterADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app, cli := s.App(), s.Client()
	recipient, run := claudeAtWork(t, s, app, "shop")
	if err := s.InjectSession("reviewer", "reviewer", s.Path("reviewer"), protocol.SessionAgentClaude); err != nil {
		t.Fatalf("register the sender: %v", err)
	}
	sent, err := cli.AgentMsg(recipient, "reviewer", "the discount is applied after tax")
	if err != nil || sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("mail to a busy agent = %+v, %v; want it queued", sent, err)
	}

	s.Stop()
	run.ReplyUnheard("Added the discount field. <!-- attn:state=idle -->")
	s.Start()
	app = s.App()
	if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("after the restart the agent was prompted with %q, want the inbox doorbell", got)
	}
	app.TypeLine(recipient, "anything new?")
	if got := run.Prompted(); got != "anything new?" {
		t.Fatalf("the agent was next prompted with %q, want the user's words and no second doorbell", got)
	}
}

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

func TestAnOutstandingInboxRingSurvivesAProcessRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app, cli := s.App(), s.Client()
	recipient, run := claudeAtWork(t, s, app, "shop")
	sender := s.Spawn(app, fakeagent.Claude, s.Path("reviewer"))
	s.Launched(sender)
	run.Reply("Ready. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, recipient, func(session protocol.Session) bool { return session.State == protocol.SessionStateIdle })
	app.AwaitScreen(recipient, "Ready. <!-- attn:state=idle -->")
	sent, err := cli.AgentMsg(recipient, protocol.SessionID(sender), "retain the outstanding attempt")
	if err != nil || sent.Status == protocol.AgentMsgStatusRefused {
		t.Fatalf("send=%+v, %v", sent, err)
	}
	if prompt := run.Prompted(); !strings.Contains(prompt, inboxDoorbell) {
		t.Fatalf("first ring=%q", prompt)
	}
	s.Stop()
	run.ReplyUnheard("Later. <!-- attn:state=idle -->")
	s.Start()
	app = s.App()
	app.TypeLine(recipient, "continue without reading")
	if prompt := run.Prompted(); prompt != "continue without reading" {
		t.Fatalf("restart renewed the outstanding ring: %q", prompt)
	}
	status, err := s.Client().AgentMsgStatus(sent.MessageID, protocol.SessionID(sender))
	if err != nil || status.State != protocol.AgentMessageStateNotified {
		t.Fatalf("durable receipt=%+v, %v", status, err)
	}
	batch, err := s.Client().AgentInboxBatch(protocol.SessionID(recipient), 0)
	if err != nil || len(batch.Items) != 1 || batch.Items[0].Address != protocol.AddressRef("session:"+recipient) {
		t.Fatalf("durable item=%+v, %v", batch, err)
	}
}

func TestRestartDuringInboxPrimingKeepsTheDayAndOutstandingDelay(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "trellis")
	s.Start()
	cli := s.Client()
	if err := s.InjectSession("reviewer", "reviewer", s.Path("reviewer"), protocol.SessionAgentClaude); err != nil {
		t.Fatal(err)
	}
	sent, err := cli.AgentMsg("trellis", "reviewer", "keep this through priming and restart")
	if err != nil || sent.Status != protocol.AgentMsgStatusQueued || protocol.Deref(sent.TargetSessionID) == "" {
		t.Fatalf("asleep send=%+v, %v", sent, err)
	}
	day := s.Launched(string(protocol.Deref(sent.TargetSessionID)))
	day.Prompted()
	s.Stop()
	s.Start()
	if bound := protocol.Deref(crewRoster(t, s)["trellis"].BindingSession); bound != protocol.Deref(sent.TargetSessionID) {
		t.Fatalf("restart woke a second day: %s, original %s", bound, protocol.Deref(sent.TargetSessionID))
	}
	day.Reply("Ready. <!-- attn:state=idle -->")
	s.App().TypeLine(string(protocol.Deref(sent.TargetSessionID)), "continue without reading")
	if prompt := day.Prompted(); prompt != "continue without reading" {
		t.Fatalf("restart renewed the outstanding wake attempt: %q", prompt)
	}
	batch, err := s.Client().AgentInboxBatch(protocol.Deref(sent.TargetSessionID), 0)
	if err != nil || len(batch.Items) != 1 || batch.Items[0].Address != "member:trellis" {
		t.Fatalf("member item=%+v, %v", batch, err)
	}
}

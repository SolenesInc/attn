package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestATriggeredNudgeWaitsUntilTheAgentCanTakeIt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	author := w.Spawn(app, fakeagent.Claude, w.Path("author"))

	asking := w.Spawn(app, fakeagent.Claude, w.Path("asking"))
	askingRun := w.Launched(asking)
	app.TypeLine(asking, "fix the build")
	askingRun.Prompted()
	if err := cli.RecordNotification(asking, "permission_prompt", "Allow edit?"); err != nil {
		t.Fatalf("ask for approval: %v", err)
	}
	testworld.AwaitSession(app, asking, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	boot := w.HoldNextBoot()
	booting := w.Spawn(app, fakeagent.Claude, w.Path("booting"))

	for _, session := range []string{booting, asking} {
		createTicket(t, cli, session, "fix the build", "ticket-"+session)
		commentOnTicket(t, cli, author, "ticket-"+session, "take a look")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TicketUnread) })
		app.Send(protocol.TriggerNudgeMessage{Cmd: protocol.CmdTriggerNudge, SessionID: session})
	}

	app.TypeLine(booting, "while you start, read the README")
	boot()
	bootedRun := w.Launched(booting)
	if first := bootedRun.Prompted(); first != "while you start, read the README" {
		t.Errorf("the session that was booting first received %q, want the user's line ahead of any doorbell", first)
	}
	bootedRun.Reply("Read it. <!-- attn:state=idle -->")
	if nudge := bootedRun.Prompted(); !strings.Contains(nudge, "attn agent inbox") {
		t.Errorf("the session that was booting received %q once idle, want the inbox doorbell", nudge)
	}

	app.TypeLine(asking, "yes, allow it")
	if answer := askingRun.Prompted(); answer != "yes, allow it" {
		t.Errorf("the session that was asking for approval first received %q, want the user's answer ahead of any doorbell", answer)
	}
	askingRun.Reply("Fixed. <!-- attn:state=idle -->")
	if nudge := askingRun.Prompted(); !strings.Contains(nudge, "attn agent inbox") {
		t.Errorf("the session that was asking for approval received %q once idle, want the inbox doorbell", nudge)
	}
}

func TestATriggeredNudgeReachesAReadyAgentWithoutWaitingOutTheCountdown(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	author := w.Spawn(app, fakeagent.Claude, w.Path("author"))
	ready := w.Spawn(app, fakeagent.Claude, w.Path("ready"))
	run := w.Launched(ready)
	app.TypeLine(ready, "fix the build")
	run.Prompted()
	run.Reply("Fixed. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, ready, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	createTicket(t, cli, ready, "fix the build", "ticket-ready")
	commentOnTicket(t, cli, author, "ticket-ready", "take a look")
	testworld.AwaitSession(app, ready, func(s protocol.Session) bool { return protocol.Deref(s.NudgeFiresAt) != "" })
	app.Send(protocol.TriggerNudgeMessage{Cmd: protocol.CmdTriggerNudge, SessionID: ready})

	if nudge := run.Prompted(); !strings.Contains(nudge, "attn agent inbox") {
		t.Errorf("the ready session received %q after the trigger, want the inbox doorbell without waiting out the countdown", nudge)
	}
}

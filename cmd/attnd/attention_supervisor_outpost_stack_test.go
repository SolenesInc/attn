package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheSupervisorOpensTheTurnOfAnOutpostAgentThatStoppedQuietly(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude)), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	home.Vars = append(home.Vars, "ATTN_HEADLESS_TASKS=on")
	home.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, "Decide whether this agent's turn should open to the user now."):
			task.Answer(`{"verdict":"OPEN","label":"unread report","reason":"the fix landed and only this session reports it"}`)
		case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
			task.Answer(`{"verdict":"DONE"}`)
		default:
			task.Answer("Refund flow")
		}
	})
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	pair.Connect()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	app := pair.App
	requireStdout(t, home.Attn("settings", "set", "queue_mode_enabled", "true"), "true")
	requireStdout(t, home.Attn("settings", "set", "attention_supervisor", "true"), "true")

	session := home.Spawn(app, fakeagent.Claude, outpost.Path("shop"), func(msg *protocol.SpawnSessionMessage) { msg.Host = &pair.Host })
	run := outpost.LaunchedCarrying(app.Terminal(session))
	app.TypeLine(session, "plan the refund flow")
	run.Prompted()
	planning := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("Planned the refund flow. <!-- attn:state=idle -->")
	owed := testworld.AwaitStateAfter(app, planning, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.TurnOwed)
	})
	app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: owed.ID})
	settled := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.StateSince == owed.StateSince && !protocol.Deref(s.TurnOwed)
	})

	sender := home.Spawn(app, fakeagent.Harness("shell"), home.Path("sender"))
	if _, err := home.Client().AgentMsg(session, protocol.SessionID(sender), "the review is in"); err != nil {
		t.Fatal(err)
	}
	outpost.Advance(sessionmonitor.QuietWindow)
	run.Prompted()
	working := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("The review found one bug, so I fixed it and landed it. <!-- attn:state=idle -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if protocol.Deref(stopped.TurnOwed) {
		t.Fatalf("the notification run's stop opened a turn: %+v", stopped)
	}

	home.AwaitTimer(daemon.AttentionStopTimer)
	home.FirePending(daemon.AttentionStopTimer)
	surfaced := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Attention != nil })
	if got := surfaced.Attention; got.Verdict != "open" || protocol.Deref(got.Label) != "unread report" || got.Reason != "the fix landed and only this session reports it" {
		t.Errorf("the outpost agent's turn carries %+v, want the open verdict and its reason", got)
	}
	text := home.Attn("state", "explain", session).Stdout
	if !strings.Contains(text, "latest verdict: open (unread report) by headless/claude-haiku-fake") || !strings.Contains(text, "supervisor") {
		t.Errorf("state explain does not print the verdict and the surface:\n%s", text)
	}
}

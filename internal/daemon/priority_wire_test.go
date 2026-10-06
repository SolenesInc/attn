package daemon_test

import (
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"os"
	"testing"
)

func TestPriorityIsBroadcastStickyAndInheritedByClear(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	id := w.Spawn(app, fakeagent.Claude, w.Path("priority"))
	agent := w.Launched(id)
	app.Send(protocol.SetSessionPriorityMessage{Cmd: protocol.CmdSetSessionPriority, SessionID: protocol.SessionID(id), Priority: true})
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.Priority) })
	app.TypeLine(id, "Finish this turn")
	agent.Prompted()
	agent.Reply("Done. <!-- attn:state=idle -->")
	next := clearClaude(app, agent, id)
	if !protocol.Deref(next.Priority) {
		t.Fatal("/clear lost priority")
	}
	w.restart()
	app = w.App()
	cli := w.Client()
	found := false
	for _, session := range app.Initial.Sessions {
		if session.ID == next.ID {
			found = protocol.Deref(session.Priority)
		}
	}
	if !found {
		t.Fatal("priority was lost across restart")
	}
	if err := cli.SetSessionPriority(next.ID, false); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, string(next.ID), func(s protocol.Session) bool { return !protocol.Deref(s.Priority) })
}

func TestPriorityDelegationAndHandover(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	source := w.Spawn(app, fakeagent.Claude, w.Path("source"))
	w.Launched(source)
	if err := os.MkdirAll(w.Path("priority"), 0o755); err != nil {
		t.Fatal(err)
	}
	request := delegateFrom(source, w.Path("priority"), "Continue this task", fakeagent.Claude)
	request.Priority = protocol.Ptr(true)
	first, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(first.SessionID)).Prompted()
	if !protocol.Deref(queriedSession(t, cli, string(first.SessionID)).Priority) {
		t.Fatal("delegate was not born marked")
	}
	next, err := cli.Delegate(seedHandoverRequest(source, first.Directory, first.SeedID, "Carry on"))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(next.SessionID)).Prompted()
	if !protocol.Deref(queriedSession(t, cli, string(next.SessionID)).Priority) {
		t.Fatal("handover lost priority")
	}
}

func TestCrewNapInheritsPriority(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := wakeCrew(t, cli, "keel", "claude")
	w.Launched(string(day.SessionID))
	if err := cli.SetSessionPriority(day.SessionID, true); err != nil {
		t.Fatal(err)
	}
	next := crewHandoff(t, cli, string(day.SessionID), "Continue the work", false, protocol.CrewDayCloseNap)
	id := protocol.Deref(next.SessionID)
	w.Launched(string(id))
	if !protocol.Deref(queriedSession(t, cli, string(id)).Priority) {
		t.Fatal("nap lost priority")
	}
}

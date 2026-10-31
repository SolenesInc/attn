package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestRepliesToACrewPartyFollowItsNextSession(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	x, xRun := mailIdleAgent(w, app, "peer")
	day := wakeCrew(t, cli, "Keel", "")
	old := string(day.SessionID)
	run := w.Launched(old)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, old, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	sendAgentMessage(t, cli, old, x, "the member sent this")
	if got := xRun.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatal(got)
	}
	batch := readInbox(t, cli, x, 0)
	if len(batch.Items) != 1 {
		t.Fatalf("member mail: %+v", batch.Items)
	}
	sender := protocol.Deref(batch.Items[0].Sender)
	if sender.Ref != "member:keel" || sender.Name != "Keel" || protocol.Deref(batch.Items[0].ReplyTo) != "Keel" {
		t.Fatalf("member sender: %+v reply to %q", sender, protocol.Deref(batch.Items[0].ReplyTo))
	}
	next := crewHandoff(t, cli, old, "Continue the conversation with our peer.", false, protocol.CrewDayCloseNap)
	id := string(protocol.Deref(next.SessionID))
	if id == "" || next.NapError != nil {
		t.Fatalf("nap: %+v", next)
	}
	nextRun := w.Launched(id)
	nextRun.Prompted()
	nextRun.Reply("Ready. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	reply := sendAgentMessage(t, cli, x, protocol.Deref(batch.Items[0].ReplyTo), "reply to the next day")
	if reply.TargetSessionID != protocol.SessionID(id) {
		t.Fatalf("reply: %+v", reply)
	}
	if got := nextRun.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatal(got)
	}
	received := readInbox(t, cli, id, 0)
	if len(received.Items) != 1 || received.Items[0].Content != "reply to the next day" {
		t.Fatalf("reply mail: %+v", received.Items)
	}
	plain := protocol.Deref(received.Items[0].Sender)
	replyTo := protocol.Deref(received.Items[0].ReplyTo)
	if plain.Ref != protocol.PartyRef("session:"+x) || replyTo == "" || !strings.HasPrefix(x, replyTo) {
		t.Fatalf("plain sender: %+v reply to %q", plain, replyTo)
	}
}

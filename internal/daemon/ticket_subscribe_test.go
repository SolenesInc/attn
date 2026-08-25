package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func callTicketSubscribe(t *testing.T, d *Daemon, sessionID, ticketID string) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	d.handleTicketSubscribe(conn, &protocol.TicketSubscribeMessage{
		Cmd:             protocol.CmdTicketSubscribe,
		SourceSessionID: sessionID,
		TicketID:        ticketID,
	})
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-subscribe response: %v", err)
	}
	return resp
}

func TestCrewMemberSubscribesAndUnsubscribesAsTheMember(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "trellis-today")
	if _, err := d.claimCrewBinding("trellis", "trellis-today"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.CreateTicket(store.Ticket{ID: "watched", Title: "Watched"}, "you", time.Now()); err != nil {
		t.Fatal(err)
	}
	if resp := callTicketSubscribe(t, d, "trellis-today", "watched"); !resp.Ok {
		t.Fatalf("subscribe response = %+v", resp)
	}
	identity := store.TicketMemberIdentity("trellis")
	if subscribed, err := d.store.IsTicketSubscribed(identity, "watched"); err != nil || !subscribed {
		t.Fatalf("member subscription = %v, %v", subscribed, err)
	}
	if subscribed, err := d.store.IsTicketSubscribed("trellis-today", "watched"); err != nil || subscribed {
		t.Fatalf("day subscription = %v, %v; want none", subscribed, err)
	}
	if resp := callTicketUnsubscribe(t, d, "trellis-today", "watched"); !resp.Ok {
		t.Fatalf("unsubscribe response = %+v", resp)
	}
	if subscribed, err := d.store.IsTicketSubscribed(identity, "watched"); err != nil || subscribed {
		t.Fatalf("member subscription after unsubscribe = %v, %v", subscribed, err)
	}
}

func callTicketUnsubscribe(t *testing.T, d *Daemon, sessionID, ticketID string) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	d.handleTicketUnsubscribe(conn, &protocol.TicketUnsubscribeMessage{
		Cmd:             protocol.CmdTicketUnsubscribe,
		SourceSessionID: sessionID,
		TicketID:        ticketID,
	})
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-unsubscribe response: %v", err)
	}
	return resp
}

func inboxHasTicket(bundles []protocol.TicketEventBundle, ticketID string) bool {
	for _, b := range bundles {
		if b.TicketID == ticketID && len(b.Events) > 0 {
			return true
		}
	}
	return false
}

// The trigger goes through commentOnTicket (synchronous), so the nudge countdown is armed
// before the assertion — unlike callSetTicketStatus, which returns before its async notify.
func TestTicketSubscribeLifecycle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agents, inputs := delegateMany(t, d, "codex", "Task Y", "Task X")
	z, x := agents[0], agents[1]
	ticketY := boundTicketID(t, d, z)
	for _, id := range agents {
		d.store.UpdateState(id, protocol.StateIdle)
	}

	if resp := callTicketSubscribe(t, d, x, ticketY); !resp.Ok ||
		resp.TicketSubscribeResult == nil || resp.TicketSubscribeResult.TicketID != ticketY {
		t.Fatalf("subscribe response = %+v, want ok echoing %s", resp, ticketY)
	}

	commentOnTicket(t, d, ticketY, "chief checking in on this thread")

	fireNudgeNow(t, d, x)
	if !wasNudged(inputs(x)) {
		t.Fatal("subscriber was not nudged about activity on the ticket it subscribed to")
	}
	nudgesAfterSubscribe := nudgeCount(inputs(x))
	if !inboxHasTicket(callTicketInbox(t, d, x), ticketY) {
		t.Fatal("subscriber's inbox did not deliver the subscribed ticket's activity")
	}

	if resp := callTicketUnsubscribe(t, d, x, ticketY); !resp.Ok {
		t.Fatalf("unsubscribe response = %+v, want ok", resp)
	}
	commentOnTicket(t, d, ticketY, "chief following up")

	if got := nudgeCount(inputs(x)); got != nudgesAfterSubscribe {
		t.Fatalf("subscriber nudged after unsubscribe: count %d -> %d", nudgesAfterSubscribe, got)
	}
	if inboxHasTicket(callTicketInbox(t, d, x), ticketY) {
		t.Fatal("unsubscribed agent still received the ticket's events in its inbox")
	}
}

func TestTicketSubscribeValidatesTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agents, _ := delegateMany(t, d, "codex", "Task Y")
	x := agents[0]

	if resp := callTicketSubscribe(t, d, x, "no-such-ticket"); resp.Ok {
		t.Fatalf("subscribe to unknown ticket returned ok: %+v", resp)
	}
	if resp := callTicketUnsubscribe(t, d, x, "no-such-ticket"); !resp.Ok {
		t.Fatalf("idempotent unsubscribe returned error: %+v", resp)
	}
}

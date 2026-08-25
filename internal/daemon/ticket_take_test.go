package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func callTicketTake(t *testing.T, d *Daemon, sessionID, ticketID string, confirm bool) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	msg := &protocol.TicketTakeMessage{
		Cmd:             protocol.CmdTicketTake,
		SourceSessionID: sessionID,
		TicketID:        ticketID,
	}
	if confirm {
		msg.Confirm = protocol.Ptr(true)
	}
	d.handleTicketTake(conn, msg)
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-take response: %v", err)
	}
	return resp
}

func TestTicketTakeOverNotifiesPreviousAssignee(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agents, inputs := delegateMany(t, d, "codex", "Task Y", "Task X")
	z, x := agents[0], agents[1] // z owns ticket Y; x owns its own ticket
	ticketY := boundTicketID(t, d, z)

	if resp := callSetTicketStatus(t, d, z, string(protocol.DispatchWorkStateInProgress), "on it"); !resp.Ok {
		t.Fatalf("z status report failed: %+v", resp)
	}
	for _, id := range agents {
		d.store.UpdateState(id, protocol.StateIdle)
	}

	if resp := callTicketTake(t, d, x, ticketY, false); resp.Ok {
		t.Fatalf("take of an assigned ticket without --confirm returned ok: %+v", resp)
	}
	if tk, _ := d.store.GetTicket(ticketY); tk == nil || tk.Assignee != z {
		t.Fatalf("ticket reassigned despite refused take: %+v", tk)
	}

	resp := callTicketTake(t, d, x, ticketY, true)
	if !resp.Ok || resp.TicketTakeResult == nil ||
		resp.TicketTakeResult.TicketID != ticketY || resp.TicketTakeResult.PreviousAssignee != z {
		t.Fatalf("confirmed take response = %+v, want ok echoing %s and previous=%s", resp, ticketY, z)
	}
	if tk, _ := d.store.GetTicket(ticketY); tk == nil || tk.Assignee != x {
		t.Fatalf("ticket not reassigned to taker: %+v", tk)
	}

	fireNudgeNow(t, d, z)
	if !wasNudged(inputs(z)) {
		t.Fatal("previous assignee was not nudged about the takeover")
	}
	if !inboxHasTicket(callTicketInbox(t, d, x), ticketY) {
		t.Fatal("taker's inbox did not deliver the taken ticket's history")
	}
}

func TestTicketTakeUnassignedSelfAndUnknown(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agents, _ := delegateMany(t, d, "codex", "Task X")
	x := agents[0]

	if _, err := d.store.CreateTicket(store.Ticket{ID: "backlog-1", Title: "spec"}, "chief", time.Now()); err != nil {
		t.Fatalf("seed backlog ticket: %v", err)
	}

	resp := callTicketTake(t, d, x, "backlog-1", false)
	if !resp.Ok || resp.TicketTakeResult == nil || resp.TicketTakeResult.PreviousAssignee != "" {
		t.Fatalf("take of unassigned ticket = %+v, want ok with empty previous assignee", resp)
	}
	if tk, _ := d.store.GetTicket("backlog-1"); tk == nil || tk.Assignee != x {
		t.Fatalf("unassigned ticket not claimed by taker: %+v", tk)
	}

	if resp := callTicketTake(t, d, x, "backlog-1", false); !resp.Ok ||
		resp.TicketTakeResult == nil || resp.TicketTakeResult.PreviousAssignee != x {
		t.Fatalf("self-take = %+v, want ok with previous=%s", resp, x)
	}
	if tk, _ := d.store.GetTicket("backlog-1"); tk == nil || tk.Assignee != x {
		t.Fatalf("self-take changed the assignee: %+v", tk)
	}

	if resp := callTicketTake(t, d, x, "no-such-ticket", true); resp.Ok {
		t.Fatalf("take of unknown ticket returned ok: %+v", resp)
	}
}

func TestCrewMemberTakeKeepsConcreteAssigneeAndDurableParticipant(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "trellis-today")
	if _, err := d.claimCrewBinding("trellis", "trellis-today"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.CreateTicket(store.Ticket{ID: "backlog-member", Title: "Member work"}, "you", time.Now()); err != nil {
		t.Fatal(err)
	}
	if resp := callTicketTake(t, d, "trellis-today", "backlog-member", false); !resp.Ok {
		t.Fatalf("take response = %+v", resp)
	}
	ticket, err := d.store.GetTicket("backlog-member")
	if err != nil || ticket == nil || ticket.Assignee != "trellis-today" {
		t.Fatalf("ticket after take = %+v, %v", ticket, err)
	}
	participants, err := d.store.TicketParticipants("backlog-member")
	if err != nil {
		t.Fatal(err)
	}
	want := store.TicketMemberIdentity("trellis")
	found := false
	for _, identity := range participants {
		found = found || identity == want
	}
	if !found {
		t.Fatalf("participants = %v, want durable %q beside the concrete assignee", participants, want)
	}
}

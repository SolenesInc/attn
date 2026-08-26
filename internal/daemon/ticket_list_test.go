package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// handleTicketList has no async side effects, so a plain syncConn suffices.
func callTicketList(t *testing.T, d *Daemon, sessionID, status string, includeArchived bool) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	msg := &protocol.TicketListMessage{Cmd: protocol.CmdTicketList}
	if sessionID != "" {
		msg.SourceSessionID = &sessionID
	}
	if status != "" {
		msg.Status = &status
	}
	if includeArchived {
		msg.IncludeArchived = &includeArchived
	}
	d.handleTicketList(conn, msg)
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-list response: %v", err)
	}
	return resp
}

func ticketsByID(tickets []protocol.Ticket) map[string]protocol.Ticket {
	out := make(map[string]protocol.Ticket, len(tickets))
	for _, t := range tickets {
		out[t.ID] = t
	}
	return out
}

func decoratedTicketUnread(t *testing.T, d *Daemon, sessionID string) bool {
	t.Helper()
	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatalf("session %s not found", sessionID)
	}
	clone := *session
	d.decorateSessionWithNudge(&clone)
	return protocol.Deref(clone.TicketUnread)
}

func TestHandleTicketListReturnsBoardWithoutSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agents, _ := delegateMany(t, d, "codex", "Task Y", "Task X")
	wantY := boundTicketID(t, d, agents[0])
	wantX := boundTicketID(t, d, agents[1])

	resp := callTicketList(t, d, "", "", false)
	if !resp.Ok || resp.TicketListResult == nil {
		t.Fatalf("ticket list response = %+v, want ok with result", resp)
	}
	got := ticketsByID(resp.TicketListResult.Tickets)
	if len(got) != 2 {
		t.Fatalf("got %d tickets, want 2: %+v", len(got), resp.TicketListResult.Tickets)
	}
	for _, id := range []string{wantY, wantX} {
		ticket, ok := got[id]
		if !ok {
			t.Fatalf("board missing ticket %s: %+v", id, resp.TicketListResult.Tickets)
		}
		if ticket.Description == "" {
			t.Fatalf("ticket %s row has empty description; list should carry the brief", id)
		}
	}
}

func TestHandleTicketListLeavesLegacyActivityForInbox(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	backend := &fakeSpawnBackend{}
	_, chiefSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: chiefSessionID,
		Brief: "Migrate the store to X", Agent: protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatal(err)
	}
	agentSession := result.SessionID
	ticketID := bindLegacyTicketTitled(t, d, agentSession, chiefSessionID, "Migrate the store to X")
	if bundles := callTicketInbox(t, d, agentSession); len(bundles) != 0 {
		t.Fatalf("initial inbox = %+v, want delivered brief consumed", bundles)
	}
	commentOnTicket(t, d, ticketID, "one more thing to check")

	resp := callTicketList(t, d, agentSession, "", false)
	if !resp.Ok || resp.TicketListResult == nil {
		t.Fatalf("ticket list response = %+v, want board", resp)
	}
	if unread, err := d.ticketUnreadForSession(agentSession); err != nil || unread == 0 {
		t.Fatalf("unread after list = %d, %v; board read consumed activity", unread, err)
	}
	if !decoratedTicketUnread(t, d, agentSession) {
		t.Fatal("ticket list cleared the app-visible unread marker")
	}

	bundles := callTicketInbox(t, d, agentSession)
	if len(bundles) != 1 || bundles[0].TicketID != ticketID || len(bundles[0].Events) != 1 {
		t.Fatalf("inbox after list = %+v, want the unread comment", bundles)
	}
	if unread, err := d.ticketUnreadForSession(agentSession); err != nil || unread != 0 {
		t.Fatalf("unread after inbox = %d, %v; consume did not acknowledge activity", unread, err)
	}
	if decoratedTicketUnread(t, d, agentSession) {
		t.Fatal("ticket inbox left the app-visible unread marker set")
	}
}

func TestHandleTicketListStatusFilter(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agents, _ := delegateMany(t, d, "codex", "Task Y", "Task X")
	reviewing := boundTicketID(t, d, agents[0])
	working := boundTicketID(t, d, agents[1])

	callSetTicketStatus(t, d, agents[0], string(protocol.DispatchWorkStateReadyForReview), "ready")

	inReview := callTicketList(t, d, "", string(protocol.TicketStatusInReview), false)
	if !inReview.Ok || inReview.TicketListResult == nil {
		t.Fatalf("in_review list response = %+v, want ok", inReview)
	}
	if got := ticketsByID(inReview.TicketListResult.Tickets); len(got) != 1 || got[reviewing].ID != reviewing {
		t.Fatalf("in_review filter returned %+v, want only %s", inReview.TicketListResult.Tickets, reviewing)
	}

	stillWorking := callTicketList(t, d, "", string(protocol.TicketStatusWorking), false)
	if got := ticketsByID(stillWorking.TicketListResult.Tickets); len(got) != 1 || got[working].ID != working {
		t.Fatalf("working filter returned %+v, want only %s", stillWorking.TicketListResult.Tickets, working)
	}
}

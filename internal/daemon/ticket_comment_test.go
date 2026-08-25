package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// syncConn never blocks, so a handler runs wholly in the caller's goroutine. A net.Pipe
// would let the test read doorbell side effects before notifyTicketObservers ran.
type syncConn struct{ buf bytes.Buffer }

func (c *syncConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *syncConn) Write(p []byte) (int, error)      { return c.buf.Write(p) }
func (c *syncConn) Close() error                     { return nil }
func (c *syncConn) LocalAddr() net.Addr              { return nil }
func (c *syncConn) RemoteAddr() net.Addr             { return nil }
func (c *syncConn) SetDeadline(time.Time) error      { return nil }
func (c *syncConn) SetReadDeadline(time.Time) error  { return nil }
func (c *syncConn) SetWriteDeadline(time.Time) error { return nil }

func callTicketComment(t *testing.T, d *Daemon, sessionID, ticketID, comment string) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	d.handleTicketComment(conn, &protocol.TicketCommentMessage{
		Cmd:             protocol.CmdTicketComment,
		SourceSessionID: sessionID,
		TicketID:        ticketID,
		Comment:         comment,
	})
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-comment response: %v", err)
	}
	return resp
}

func TestHandleTicketCommentValidatesTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agents, _ := delegateMany(t, d, "codex", "Task Y", "Task X")
	z, x := agents[0], agents[1]
	ticketY := boundTicketID(t, d, z)

	resp := callTicketComment(t, d, x, ticketY, "looks good to me")
	if !resp.Ok || resp.TicketCommentResult == nil || resp.TicketCommentResult.TicketID != ticketY {
		t.Fatalf("comment response = %+v, want ok echoing %s", resp, ticketY)
	}

	if bad := callTicketComment(t, d, x, "no-such-ticket", "hi"); bad.Ok {
		t.Fatalf("comment on unknown ticket returned ok: %+v", bad)
	}
}

func TestCrewMemberCommentIsAttributedToTheMemberWithoutSubscribingIt(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "trellis-today")
	if _, err := d.claimCrewBinding("trellis", "trellis-today"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.CreateTicket(store.Ticket{ID: "peer-ticket", Title: "Peer"}, "you", time.Now()); err != nil {
		t.Fatal(err)
	}
	if resp := callTicketComment(t, d, "trellis-today", "peer-ticket", "one useful note"); !resp.Ok {
		t.Fatalf("comment response = %+v", resp)
	}
	ticket, err := d.store.GetTicket("peer-ticket")
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket = %+v, %v", ticket, err)
	}
	last := ticket.Activity[len(ticket.Activity)-1]
	identity := store.TicketMemberIdentity("trellis")
	if last.Author != identity {
		t.Fatalf("comment author = %q, want %q", last.Author, identity)
	}
	participants, err := d.store.TicketParticipants("peer-ticket")
	if err != nil {
		t.Fatal(err)
	}
	for _, participant := range participants {
		if participant == identity {
			t.Fatalf("comment-only member became a participant: %v", participants)
		}
	}
}

func TestAgentCommentDoesNotSubscribeCommenter(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agents, inputs := delegateMany(t, d, "codex", "Task Y", "Task X")
	z, x := agents[0], agents[1]
	ticketY := boundTicketID(t, d, z)
	for _, id := range agents {
		d.store.UpdateState(id, protocol.StateIdle)
	}

	resp := callTicketComment(t, d, x, ticketY, "looks good, ship it")
	if !resp.Ok {
		t.Fatalf("comment response = %+v, want ok", resp)
	}

	fireNudgeNow(t, d, z)
	if !wasNudged(inputs(z)) {
		t.Fatal("assignee was not nudged about the comment on its ticket")
	}
	if wasNudged(inputs(x)) {
		t.Fatal("commenter was nudged about its own comment")
	}

	callSetTicketStatus(t, d, z, string(protocol.DispatchWorkStateReadyForReview), "done")
	if wasNudged(inputs(x)) {
		t.Fatal("commenter was nudged by a later event on a ticket it only commented on")
	}
	for _, b := range callTicketInbox(t, d, x) {
		if b.TicketID == ticketY {
			t.Fatalf("commenter's inbox carried events for a ticket it only commented on: %+v", b)
		}
	}
}

func TestStandaloneTicketCreatorGetsUnreadCommentIndicator(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, creatorID, _ := delegateForNotify(t, d, "codex")
	d.store.UpdateState(creatorID, protocol.StateIdle)
	d.setSelectedSession(creatorID)
	ticket, err := d.store.CreateTicket(store.Ticket{
		ID: "standalone", Title: "Standalone", Status: store.TicketStatusTodo,
	}, creatorID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	commenterID := "commenter"
	d.store.Add(&protocol.Session{ID: commenterID, Label: commenterID, Agent: protocol.SessionAgentShell, Directory: t.TempDir(), State: protocol.StateWorking})

	resp := callTicketComment(t, d, commenterID, ticket.ID, "please review")
	if !resp.Ok {
		t.Fatalf("comment response = %+v", resp)
	}
	decorated := d.sessionForBroadcast(d.store.Get(creatorID))
	if decorated == nil || !protocol.Deref(decorated.TicketUnread) {
		t.Fatalf("creator session = %+v, want unread ticket indicator", decorated)
	}
	if decorated.NudgeFiresAt != nil {
		t.Fatalf("selected creator armed countdown at %s", *decorated.NudgeFiresAt)
	}
}

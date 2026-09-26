package daemon

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func boundTicketID(t *testing.T, d *Daemon, sessionID string) string {
	t.Helper()
	ticket, err := d.store.ActiveTicketForSession(sessionID)
	if err != nil {
		t.Fatalf("ActiveTicketForSession: %v", err)
	}
	if ticket == nil {
		t.Fatal("session has no bound ticket")
	}
	return ticket.ID
}

func TestReapAfterRestartHonorsIntentionalClose(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	d.store.UpdateState(sessionID, protocol.StateWorking)

	d.terminateSession(sessionID, syscall.SIGTERM)

	d2 := NewForTesting(filepath.Join(t.TempDir(), "restart.sock"))
	d2.store = d.store
	d2.removeReapedSession(sessionID)

	ticket, err := d2.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status == store.TicketStatusCrashed {
		t.Fatal("restart reap crash-stamped an intentionally closed session's ticket")
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status = %q, want unchanged working", ticket.Status)
	}
}

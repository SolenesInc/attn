package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func TestRecoveryAdoptRevivesCrashedTicketToWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	session := d.store.Get(sessionID)

	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateWorking)
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after crash: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status after crash = %q, want crashed", ticket.Status)
	}

	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{sessionID},
		info: map[string]ptybackend.SessionInfo{
			sessionID: {
				SessionID: sessionID,
				Agent:     string(session.Agent),
				CWD:       session.Directory,
				Running:   true,
				State:     protocol.StateWorking,
			},
		},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})

	ticket, err = d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after adopt: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status after adopt = %q, want working", ticket.Status)
	}
}

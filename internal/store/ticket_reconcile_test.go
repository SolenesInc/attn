package store

import (
	"testing"
	"time"
)

func TestClearTicketReconciliationForAssignee(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	for _, id := range []string{"flag-a", "flag-b"} {
		if _, err := s.CreateTicket(Ticket{ID: id, Title: "t", Assignee: "sess-1", Status: TicketStatusWorking}, "chief", ticketBase); err != nil {
			t.Fatalf("CreateTicket %s: %v", id, err)
		}
		if _, err := s.ClaimTicketReconciliation(id, ticketBase.Add(time.Hour)); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}

	if err := s.ClearTicketReconciliationForAssignee("sess-1"); err != nil {
		t.Fatalf("ClearTicketReconciliationForAssignee: %v", err)
	}
	for _, id := range []string{"flag-a", "flag-b"} {
		got, err := s.GetTicket(id)
		if err != nil || got == nil {
			t.Fatalf("GetTicket %s: %v, %v", id, got, err)
		}
		if got.ReconciledAt != nil {
			t.Fatalf("%s ReconciledAt = %v, want cleared", id, got.ReconciledAt)
		}
	}

	claimed, err := s.ClaimTicketReconciliation("flag-a", ticketBase.Add(2*time.Hour))
	if err != nil || !claimed {
		t.Fatalf("re-claim after clear = %v, %v; want true, nil", claimed, err)
	}
}

func TestAssignTicketClearsReconciliation(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "retaken", Title: "t", Assignee: "sess-1", Status: TicketStatusWorking}, "chief", ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := s.ClaimTicketReconciliation("retaken", ticketBase.Add(time.Hour)); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := s.AssignTicket("retaken", "sess-2", "sess-2", ticketBase.Add(2*time.Hour)); err != nil {
		t.Fatalf("AssignTicket: %v", err)
	}
	got, err := s.GetTicket("retaken")
	if err != nil || got == nil {
		t.Fatalf("GetTicket: %v, %v", got, err)
	}
	if got.Assignee != "sess-2" {
		t.Fatalf("Assignee = %q, want sess-2", got.Assignee)
	}
	if got.ReconciledAt != nil {
		t.Fatalf("ReconciledAt = %v, want cleared on reassign (re-arm)", got.ReconciledAt)
	}
}

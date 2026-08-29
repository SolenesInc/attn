package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

func TestAutomationTicketRetentionSweepPassHonoursEnvOverrideTTL(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_TICKET_RETENTION_TTL", "1h")

	s := store.New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	if _, err := s.CreateTicket(store.Ticket{ID: "open", Title: "live"}, "you", now.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("create open: %v", err)
	}
	if _, err := s.CreateTicket(store.Ticket{ID: "user-stale", Title: "user work"}, "you", now.Add(-3*time.Hour)); err != nil {
		t.Fatalf("create user ticket: %v", err)
	}
	if _, err := s.SetTicketStatus("user-stale", store.TicketStatusDone, "agent7", "done", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("close user ticket: %v", err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{
		ID: "automation-stale", Title: "scheduled work", Status: store.TicketStatusDone,
		AutomationRunID: "run-stale",
	}, "automation:nightly", "chief-of-staff", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("create automation ticket: %v", err)
	}

	d := &Daemon{store: s}
	d.automationTicketRetentionSweepPass(now)

	if gone, _ := s.GetTicket("automation-stale"); gone != nil {
		t.Fatal("expired automation ticket survived the sweep pass")
	}
	if kept, _ := s.GetTicket("user-stale"); kept == nil {
		t.Fatal("old terminal user ticket was swept")
	}
	if kept, _ := s.GetTicket("open"); kept == nil {
		t.Fatal("open backlog ticket was swept")
	}
}

func TestAutomationTicketRetentionSweepPassGuardsNilStore(t *testing.T) {
	d := &Daemon{}
	d.automationTicketRetentionSweepPass(time.Now())
}

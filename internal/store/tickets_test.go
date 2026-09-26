package store

import (
	"reflect"
	"testing"
	"time"
)

var ticketBase = time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)

func TestAutomationTicketRetentionKeepsUserHistoryForever(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	const ttl = 30 * 24 * time.Hour
	now := ticketBase

	if _, err := s.CreateTicket(Ticket{ID: "open", Title: "live"}, "you", now.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("create open: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "recent-user", Title: "recent", Status: TicketStatusDone}, "you", now.Add(-5*24*time.Hour)); err != nil {
		t.Fatalf("create recent user ticket: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "stale-user", Title: "stale"}, "you", now.Add(-100*24*time.Hour)); err != nil {
		t.Fatalf("create stale user ticket: %v", err)
	}
	closedAt := now.Add(-60 * 24 * time.Hour)
	if _, err := s.SetTicketStatus("stale-user", TicketStatusDone, "agent7", "done long ago", closedAt); err != nil {
		t.Fatalf("close stale user ticket: %v", err)
	}
	if _, err := s.AddTicketAttachment(TicketAttachment{TicketID: "stale-user", Filename: "old.txt"}, "agent7", closedAt); err != nil {
		t.Fatalf("attach stale user ticket: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "auto-0123456789abcdef", Title: "human despite its id"}, "you", now.Add(-100*24*time.Hour)); err != nil {
		t.Fatalf("create automation-shaped user ticket: %v", err)
	}
	if _, err := s.SetTicketStatus("auto-0123456789abcdef", TicketStatusFailed, "agent7", "failed long ago", closedAt); err != nil {
		t.Fatalf("close automation-shaped user ticket: %v", err)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{
		ID: "stale-automation", Title: "scheduled work", Status: TicketStatusDone,
		AutomationRunID: "run-stale",
	}, "automation:nightly", TicketRoleChiefOfStaff, closedAt); err != nil {
		t.Fatalf("create stale automation ticket: %v", err)
	}
	if _, err := s.AddTicketAttachment(TicketAttachment{TicketID: "stale-automation", Filename: "automation.txt"}, "automation:nightly", closedAt); err != nil {
		t.Fatalf("attach stale automation ticket: %v", err)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{
		ID: "recent-automation", Title: "recent scheduled work", Status: TicketStatusDone,
		AutomationRunID: "run-recent",
	}, "automation:nightly", TicketRoleChiefOfStaff, now.Add(-5*24*time.Hour)); err != nil {
		t.Fatalf("create recent automation ticket: %v", err)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{
		ID: "open-automation", Title: "open scheduled work", Status: TicketStatusWorking,
		AutomationRunID: "run-open",
	}, "automation:nightly", TicketRoleChiefOfStaff, now.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("create open automation ticket: %v", err)
	}

	userBefore, err := s.GetTicket("stale-user")
	if err != nil {
		t.Fatalf("read stale user ticket: %v", err)
	}
	childCounts := func(ticketID string) []int {
		t.Helper()
		counts := make([]int, 0, 5)
		for _, table := range []string{"ticket_activity", "ticket_attachments", "ticket_events", "ticket_event_cursors", "ticket_subscriptions"} {
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE ticket_id=?`, ticketID).Scan(&count); err != nil {
				t.Fatalf("count %s for %s: %v", table, ticketID, err)
			}
			counts = append(counts, count)
		}
		return counts
	}
	userChildrenBefore := childCounts("stale-user")

	removed, err := s.SweepExpiredAutomationTickets(now, ttl)
	if err != nil {
		t.Fatalf("SweepExpiredAutomationTickets: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 stale automation ticket", removed)
	}

	if gone, _ := s.GetTicket("stale-automation"); gone != nil {
		t.Fatal("stale automation ticket survived the sweep")
	}
	if kept, _ := s.GetTicket("recent-automation"); kept == nil {
		t.Fatal("recent automation ticket was swept early")
	}
	if kept, _ := s.GetTicket("open-automation"); kept == nil {
		t.Fatal("open automation ticket was swept")
	}
	if kept, _ := s.GetTicket("open"); kept == nil {
		t.Fatal("open backlog ticket was swept")
	}
	if kept, _ := s.GetTicket("auto-0123456789abcdef"); kept == nil {
		t.Fatal("automation-shaped user ticket was swept without provenance")
	}
	userAfter, err := s.GetTicket("stale-user")
	if err != nil {
		t.Fatalf("read stale user ticket after sweep: %v", err)
	}
	if !reflect.DeepEqual(userAfter, userBefore) {
		t.Fatalf("stale user ticket changed:\nbefore: %#v\nafter:  %#v", userBefore, userAfter)
	}
	if after := childCounts("stale-user"); !reflect.DeepEqual(after, userChildrenBefore) {
		t.Fatalf("stale user child rows changed: before=%v after=%v", userChildrenBefore, after)
	}
	if children := childCounts("stale-automation"); !reflect.DeepEqual(children, []int{0, 0, 0, 0, 0}) {
		t.Fatalf("expired automation ticket left child rows: %v", children)
	}
}

func TestTicketAssigneesOwnedByRole(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if ids := s.TicketAssigneesOwnedByRole(""); len(ids) != 0 {
		t.Fatalf("empty role = %v, want none", ids)
	}

	if _, err := s.CreateRoleOwnedTicket(Ticket{
		ID:       "delegated",
		Title:    "do the thing",
		Assignee: "sess-delegated",
	}, "chief-1", TicketRoleChiefOfStaff, ticketBase); err != nil {
		t.Fatalf("CreateTicket delegated: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{
		ID:       "self-authored",
		Title:    "user work",
		Assignee: "sess-other",
	}, "you", ticketBase); err != nil {
		t.Fatalf("CreateTicket self: %v", err)
	}

	ids := s.TicketAssigneesOwnedByRole(TicketRoleChiefOfStaff)
	if !ids["sess-delegated"] {
		t.Fatalf("delegated session missing from set: %v", ids)
	}
	if ids["sess-other"] {
		t.Fatalf("non-chief-authored session leaked into set: %v", ids)
	}

	if _, err := s.SetTicketStatus("delegated", TicketStatusDone, "sess-delegated", "shipped", ticketBase.Add(time.Minute)); err != nil {
		t.Fatalf("SetTicketStatus terminal: %v", err)
	}
	if ids := s.TicketAssigneesOwnedByRole(TicketRoleChiefOfStaff); !ids["sess-delegated"] {
		t.Fatalf("delegated session lost after terminal report: %v", ids)
	}

	if err := s.ArchiveTicket("delegated", ticketBase.Add(2*time.Minute)); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	if ids := s.TicketAssigneesOwnedByRole(TicketRoleChiefOfStaff); ids["sess-delegated"] {
		t.Fatalf("archived ticket still in set: %v", ids)
	}
}

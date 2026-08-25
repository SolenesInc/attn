package store

import (
	"errors"
	"testing"
	"time"
)

// An identity that is neither assignee nor author becomes a participant by subscribing,
// and is served the backlog too, since subscribing does not advance the cursor.
func TestTicketSubscriptionMakesParticipant(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	tick := eventBase
	next := func() time.Time { tick = tick.Add(time.Minute); return tick }

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work", Assignee: "agent7"}, "chief", next()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := s.SetTicketStatus("tk", TicketStatusInReview, "agent7", "ready", next()); err != nil {
		t.Fatalf("SetTicketStatus: %v", err)
	}

	if got, err := s.UnreadTicketEvents("watcher"); err != nil || len(got) != 0 {
		t.Fatalf("pre-subscribe unread = %d (err %v), want 0", len(got), err)
	}

	if err := s.AddTicketSubscription("watcher", "tk", next()); err != nil {
		t.Fatalf("AddTicketSubscription: %v", err)
	}

	parts, err := s.TicketParticipants("tk")
	if err != nil {
		t.Fatalf("TicketParticipants: %v", err)
	}
	if !contains(parts, "watcher") {
		t.Fatalf("participants = %v, want to include watcher", parts)
	}
	unread, err := s.UnreadTicketEvents("watcher")
	if err != nil {
		t.Fatalf("UnreadTicketEvents: %v", err)
	}
	if len(unread) == 0 {
		t.Fatal("subscriber has 0 unread events, want the ticket's backlog")
	}
	for _, e := range unread {
		if e.TicketID != "tk" {
			t.Fatalf("subscriber unread carried foreign ticket %q", e.TicketID)
		}
	}

	if err := s.RemoveTicketSubscription("watcher", "tk"); err != nil {
		t.Fatalf("RemoveTicketSubscription: %v", err)
	}
	if parts, _ := s.TicketParticipants("tk"); contains(parts, "watcher") {
		t.Fatalf("participants = %v, want watcher removed after unsubscribe", parts)
	}
	if got, err := s.UnreadTicketEvents("watcher"); err != nil || len(got) != 0 {
		t.Fatalf("post-unsubscribe unread = %d (err %v), want 0", len(got), err)
	}
}

func TestSubscribeIdempotentAndValidatesTicket(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	now := eventBase
	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, "chief", now); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	if err := s.AddTicketSubscription("watcher", "tk", now); err != nil {
		t.Fatalf("first subscribe: %v", err)
	}
	if err := s.AddTicketSubscription("watcher", "tk", now); err != nil {
		t.Fatalf("second subscribe: %v", err)
	}
	if ok, err := s.IsTicketSubscribed("watcher", "tk"); err != nil || !ok {
		t.Fatalf("IsTicketSubscribed = (%v, %v), want (true, nil)", ok, err)
	}

	if err := s.AddTicketSubscription("watcher", "ghost", now); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("subscribe to missing ticket = %v, want ErrTicketNotFound", err)
	}

	if err := s.RemoveTicketSubscription("watcher", "tk"); err != nil {
		t.Fatalf("first unsubscribe: %v", err)
	}
	if err := s.RemoveTicketSubscription("watcher", "tk"); err != nil {
		t.Fatalf("idempotent unsubscribe: %v", err)
	}
	if err := s.RemoveTicketSubscription("nobody", "ghost"); err != nil {
		t.Fatalf("unsubscribe from never-subscribed: %v", err)
	}
	if ok, err := s.IsTicketSubscribed("watcher", "tk"); err != nil || ok {
		t.Fatalf("IsTicketSubscribed after unsubscribe = (%v, %v), want (false, nil)", ok, err)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

package store

import (
	"testing"
	"time"
)

func bell(recipient, id string) GardenSeedBellDelivery {
	return GardenSeedBellDelivery{RecipientSessionID: recipient, ItemID: id}
}

func TestGardenSeedWatchAndDurableBellLifecycle(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()

	changed, err := s.SetGardenSeedWatch("watcher", "s-7k3f9m", true, now)
	if err != nil || !changed {
		t.Fatalf("first watch changed=%v err=%v", changed, err)
	}
	changed, err = s.SetGardenSeedWatch("watcher", "s-7k3f9m", true, now)
	if err != nil || changed {
		t.Fatalf("repeated watch changed=%v err=%v", changed, err)
	}

	created, handled, err := s.HandleGardenSeedEvent(1, "s-7k3f9m", "event", "seed activity", []GardenSeedBellDelivery{bell("watcher", "bell-1")}, now)
	if err != nil || !handled || len(created) != 1 || created[0] != "watcher" {
		t.Fatalf("first event created=%v handled=%v err=%v", created, handled, err)
	}
	created, handled, err = s.HandleGardenSeedEvent(2, "s-7k3f9m", "event", "seed activity", []GardenSeedBellDelivery{bell("watcher", "bell-2")}, now.Add(time.Second))
	if err != nil || !handled || len(created) != 0 {
		t.Fatalf("coalesced event created=%v handled=%v err=%v", created, handled, err)
	}

	items, err := s.UnreadGardenSeedMailboxItems("watcher")
	if err != nil || len(items) != 1 || items[0].BellName != "seed activity" {
		t.Fatalf("pending items=%+v err=%v", items, err)
	}
	consumed, remaining, err := s.ReadGardenSeedMailboxItems("watcher", "s-7k3f9m", now)
	if err != nil || !consumed || remaining != 0 {
		t.Fatalf("consume=%v remaining=%d err=%v", consumed, remaining, err)
	}

	created, handled, err = s.HandleGardenSeedEvent(1, "s-7k3f9m", "event", "seed activity", []GardenSeedBellDelivery{bell("watcher", "replay")}, now.Add(2*time.Second))
	if err != nil || handled || len(created) != 0 {
		t.Fatalf("replay created=%v handled=%v err=%v", created, handled, err)
	}
	if unread, err := s.HasUnreadAgentMailboxItems("watcher"); err != nil || unread {
		t.Fatalf("replay recreated a read bell: unread=%v err=%v", unread, err)
	}
}

func TestGardenSeedUnblockedEventPromotesThePendingHint(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()
	if _, _, err := s.HandleGardenSeedEvent(1, "s-7k3f9m", "note.added", "seed activity", []GardenSeedBellDelivery{
		bell("watcher", "note-bell"),
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.HandleGardenSeedEvent(2, "s-7k3f9m", "unblocked", "seed activity", []GardenSeedBellDelivery{
		bell("watcher", "unblocked-bell"),
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var hint, bellName string
	if err := s.db.QueryRow(`SELECT hint, bell_name FROM agent_mailbox_items WHERE id = 'note-bell'`).Scan(&hint, &bellName); err != nil {
		t.Fatal(err)
	}
	if hint != "unblocked" || bellName != "seed activity" {
		t.Fatalf("coalesced bell hint=%q definition=%q", hint, bellName)
	}
}

func TestGardenSeedEventQuietAndEmptyAudienceReceiptsAreFinal(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()
	for _, test := range []struct {
		seq  int64
		bell string
	}{{1, ""}, {2, "seed activity"}} {
		created, handled, err := s.HandleGardenSeedEvent(test.seq, "s-7k3f9m", "event", test.bell, nil, now)
		if err != nil || !handled || len(created) != 0 {
			t.Fatalf("event %d created=%v handled=%v err=%v", test.seq, created, handled, err)
		}
		_, handled, err = s.HandleGardenSeedEvent(test.seq, "s-7k3f9m", "event", test.bell, nil, now)
		if err != nil || handled {
			t.Fatalf("replay event %d handled=%v err=%v", test.seq, handled, err)
		}
	}
}

func TestGardenSeedEventFailureRollsBackReceiptAndAllRecipients(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.db.Exec(`CREATE TRIGGER reject_bob BEFORE INSERT ON agent_mailbox_items
		WHEN NEW.recipient_session_id = 'bob' BEGIN SELECT RAISE(ABORT, 'bob rejected'); END`); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.HandleGardenSeedEvent(1, "s-7k3f9m", "event", "seed activity", []GardenSeedBellDelivery{
		bell("alice", "bell-a"), bell("bob", "bell-b"),
	}, time.Now())
	if err == nil {
		t.Fatal("recipient failure ignored")
	}
	for table, want := range map[string]int{"garden_seed_event_receipts": 0, "agent_mailbox_items": 0} {
		var got int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s rows=%d err=%v", table, got, err)
		}
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_bob`); err != nil {
		t.Fatal(err)
	}
	created, handled, err := s.HandleGardenSeedEvent(1, "s-7k3f9m", "event", "seed activity", []GardenSeedBellDelivery{
		bell("alice", "bell-a"), bell("bob", "bell-b"),
	}, time.Now())
	if err != nil || !handled || len(created) != 2 {
		t.Fatalf("retry created=%v handled=%v err=%v", created, handled, err)
	}
}

func TestGardenSeedEventReceiptFailureRollsBackMailbox(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.db.Exec(`CREATE TRIGGER reject_receipt BEFORE INSERT ON garden_seed_event_receipts
		BEGIN SELECT RAISE(ABORT, 'receipt rejected'); END`); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.HandleGardenSeedEvent(1, "s-7k3f9m", "event", "seed activity", []GardenSeedBellDelivery{bell("alice", "bell-a")}, time.Now())
	if err == nil {
		t.Fatal("receipt failure ignored")
	}
	var mailbox int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_mailbox_items`).Scan(&mailbox); err != nil || mailbox != 0 {
		t.Fatalf("mailbox rows=%d err=%v", mailbox, err)
	}
}

func TestPendingGardenSeedBellDefinitions(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if _, _, err := s.HandleGardenSeedEvent(1, "s-7k3f9m", "event", "seed activity", []GardenSeedBellDelivery{bell("alice", "bell-a")}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, err := s.PendingGardenSeedBellNames(); err != nil || len(got) != 1 || got[0] != "seed activity" {
		t.Fatalf("definitions=%v err=%v", got, err)
	}
	if got, err := s.PendingGardenSeedMailboxItems(); err != nil || len(got) != 1 || got[0].RecipientSessionID != "alice" {
		t.Fatalf("pending=%+v err=%v", got, err)
	}
}

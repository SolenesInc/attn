package store

import (
	"testing"
	"time"
)

func TestGardenSeedWatchAndBellLifecycle(t *testing.T) {
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
	watching, err := s.GardenSeedWatching("watcher", "s-7k3f9m")
	if err != nil || !watching {
		t.Fatalf("watching=%v err=%v", watching, err)
	}

	claimed, err := s.ClaimGardenSeedMailboxItem("watcher", "s-7k3f9m", "note", "bell-1", now)
	if err != nil || !claimed {
		t.Fatalf("first bell claimed=%v err=%v", claimed, err)
	}
	claimed, err = s.ClaimGardenSeedMailboxItem("watcher", "s-7k3f9m", "harvested", "bell-2", now.Add(time.Second))
	if err != nil || claimed {
		t.Fatalf("coalesced bell claimed=%v err=%v", claimed, err)
	}
	var itemID, sourceID string
	if err := s.db.QueryRow(`
		SELECT id, source_id FROM agent_mailbox_items
		WHERE recipient_session_id = 'watcher' AND read_at = ''
	`).Scan(&itemID, &sourceID); err != nil || itemID != "bell-1" || sourceID != "s-7k3f9m" {
		t.Fatalf("unread bell=%s/%s err=%v", itemID, sourceID, err)
	}

	consumed, remaining, err := s.ReadGardenSeedMailboxItems("watcher", "s-7k3f9m", now)
	if err != nil || !consumed {
		t.Fatalf("consume=%v err=%v", consumed, err)
	}
	if remaining != 0 {
		t.Fatalf("remaining=%d, want 0", remaining)
	}
	unread, err := s.HasUnreadAgentMailboxItems("watcher")
	if err != nil || unread {
		t.Fatalf("unread after read=%v err=%v", unread, err)
	}
	claimed, err = s.ClaimGardenSeedMailboxItem("watcher", "s-7k3f9m", "harvested", "bell-2", now.Add(time.Second))
	if err != nil || !claimed {
		t.Fatalf("bell after read claimed=%v err=%v", claimed, err)
	}

	changed, err = s.SetGardenSeedWatch("watcher", "s-7k3f9m", false, now)
	if err != nil || !changed {
		t.Fatalf("unwatch changed=%v err=%v", changed, err)
	}
}

func TestGardenUnblockedBellPromotesTheCoalescedEvent(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()

	claimed, err := s.ClaimGardenSeedMailboxItem("worker", "s-7k3f9m", "note", "bell-1", now)
	if err != nil || !claimed {
		t.Fatalf("first bell claimed=%v err=%v", claimed, err)
	}
	claimed, err = s.ClaimGardenSeedMailboxItem(
		"worker", "s-7k3f9m", GardenSeedEventUnblocked, "bell-2", now.Add(time.Second))
	if err != nil || claimed {
		t.Fatalf("unblocked bell claimed=%v err=%v", claimed, err)
	}
	claimed, err = s.ClaimGardenSeedMailboxItem("worker", "s-7k3f9m", "note", "bell-3", now.Add(2*time.Second))
	if err != nil || claimed {
		t.Fatalf("later note claimed=%v err=%v", claimed, err)
	}

	items, err := s.UnreadGardenSeedMailboxItems("worker")
	if err != nil || len(items) != 1 {
		t.Fatalf("mailbox items = %+v, %v", items, err)
	}
	if items[0].SeedID != "s-7k3f9m" || items[0].EventKind != GardenSeedEventUnblocked {
		t.Fatalf("coalesced item = %+v, want one promoted unblocked event", items[0])
	}
}

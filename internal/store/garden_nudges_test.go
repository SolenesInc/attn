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

	_, claimed, err := s.ClaimGardenSeedMailboxItem("watcher", "s-7k3f9m", "note", "bell-1", now)
	if err != nil || !claimed {
		t.Fatalf("first bell claimed=%v err=%v", claimed, err)
	}
	_, claimed, err = s.ClaimGardenSeedMailboxItem("watcher", "s-7k3f9m", "harvested", "bell-2", now.Add(time.Second))
	if err != nil || claimed {
		t.Fatalf("coalesced bell claimed=%v err=%v", claimed, err)
	}
	queued, err := s.QueuedAgentMailboxDeliveries("watcher")
	if err != nil || len(queued) != 1 || queued[0].Item.ID != "bell-1" || queued[0].Item.SourceID != "s-7k3f9m" {
		t.Fatalf("queued bells=%+v err=%v", queued, err)
	}

	consumed, err := s.ReadGardenSeedMailboxItems("watcher", "s-7k3f9m", now)
	if err != nil || !consumed {
		t.Fatalf("consume=%v err=%v", consumed, err)
	}
	queued, err = s.QueuedAgentMailboxDeliveries("watcher")
	if err != nil || len(queued) != 0 {
		t.Fatalf("queued after read=%+v err=%v", queued, err)
	}
	_, claimed, err = s.ClaimGardenSeedMailboxItem("watcher", "s-7k3f9m", "harvested", "bell-2", now.Add(time.Second))
	if err != nil || !claimed {
		t.Fatalf("bell after read claimed=%v err=%v", claimed, err)
	}

	changed, err = s.SetGardenSeedWatch("watcher", "s-7k3f9m", false, now)
	if err != nil || !changed {
		t.Fatalf("unwatch changed=%v err=%v", changed, err)
	}
}

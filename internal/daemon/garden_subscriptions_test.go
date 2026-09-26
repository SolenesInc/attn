package daemon

import (
	"database/sql"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestSeedNudges_UnwatchRacingEventAndInboxLeavesNoPendingUpdate(t *testing.T) {
	f := newSeededNudgeGarden(t)
	d := f.d
	watchSeed(t, d, "sess-b", f.crown.ID, false)
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(3)
	go func() { defer workers.Done(); <-start; d.ringSeedActivity(f.leaf.ID, "note", "sess-a") }()
	go func() { defer workers.Done(); <-start; watchSeed(t, d, "sess-b", f.crown.ID, true) }()
	go func() {
		defer workers.Done()
		<-start
		resp := gardenCall(t, func(c net.Conn) { d.handleAgentInboxBatch(c, "sess-b", 0) })
		if !resp.Ok {
			t.Errorf("inbox: %+v", resp)
		}
	}()
	close(start)
	workers.Wait()
	if got := queuedSeedBells(t, d, "sess-b"); len(got) != 0 {
		t.Fatalf("racing enqueue survived unwatch: %v", got)
	}
	d.ringSeedActivity(f.leaf.ID, "note", "sess-a")
	if got := queuedSeedBells(t, d, "sess-b"); len(got) != 0 {
		t.Fatalf("post-unwatch enqueue: %v", got)
	}
}

func persistentSubscriptionGarden(t *testing.T) (*Daemon, *sql.DB) {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	d.stopEventBus()
	if err := d.store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "subscriptions.db")
	persistent, err := store.NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	d.store = persistent
	d.eventBus = nil
	d.ensureEventBus()
	d.ensureGardenCollections()
	direct, err := store.OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.stopAgentMailboxDoorbells(); d.stopEventBus(); direct.Close(); persistent.Close() })
	addGardenSession(t, d, "planner")
	addGardenSession(t, d, "worker")
	return d, direct
}

func TestSeedNudges_UnwatchCleanupFailureRemainsRetryable(t *testing.T) {
	d, db := persistentSubscriptionGarden(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "retryable unwatch"})
	watchSeed(t, d, "planner", seed.ID, false)
	if _, err := claimGardenSeedMailboxItemForTest(d.store, "planner", seed.ID, "note", "pending", time.Now()); err != nil {
		t.Fatal(err)
	}
	d.noteQueuedAgentMailboxItem("planner")
	if _, err := db.Exec(`CREATE TRIGGER refuse_discard BEFORE UPDATE OF read_at ON agent_mailbox_items BEGIN SELECT RAISE(ABORT, 'cleanup unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.setSeedWatch("planner", seed.ID, false); err == nil || !strings.Contains(err.Error(), "retry unwatch") {
		t.Fatalf("unwatch error = %v", err)
	}
	if watching, err := d.store.GardenSeedWatching("planner", seed.ID); err != nil || watching {
		t.Fatalf("deletion did not persist: %v %v", watching, err)
	}
	if bells := queuedSeedBells(t, d, "planner"); len(bells) != 1 {
		t.Fatalf("failed cleanup lost retryable item: %v", bells)
	}
	if _, err := db.Exec(`DROP TRIGGER refuse_discard`); err != nil {
		t.Fatal(err)
	}
	result, err := d.setSeedWatch("planner", seed.ID, false)
	if err != nil || result.Changed || result.Watching {
		t.Fatalf("retry = %+v, %v", result, err)
	}
	if bells := queuedSeedBells(t, d, "planner"); len(bells) != 0 {
		t.Fatalf("retry did not clear item: %v", bells)
	}
	if d.hasQueuedAgentMailboxItems("planner") {
		t.Fatal("retry left a doorbell armed")
	}
}

func TestSeedNudges_UnwatchRetryRefreshesAnAlreadyClearedQueue(t *testing.T) {
	d, _ := persistentSubscriptionGarden(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "retry unread refresh"})
	watchSeed(t, d, "planner", seed.ID, false)
	if _, err := claimGardenSeedMailboxItemForTest(d.store, "planner", seed.ID, "note", "pending", time.Now()); err != nil {
		t.Fatal(err)
	}
	d.noteQueuedAgentMailboxItem("planner")
	if _, err := d.store.SetGardenSeedWatch("planner", seed.ID, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := d.store.DiscardGardenSeedMailboxItems("planner", []string{seed.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	result, err := d.setSeedWatch("planner", seed.ID, false)
	if err != nil || result.Changed || result.Watching {
		t.Fatalf("retry = %+v, %v", result, err)
	}
	if d.hasQueuedAgentMailboxItems("planner") {
		t.Fatal("retry left an empty doorbell armed")
	}
}

func TestGardenDispatchSubscriptionFailureSurfacesAndLeavesNoBinding(t *testing.T) {
	d, db := persistentSubscriptionGarden(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "atomic delegation"})
	if _, err := db.Exec(`CREATE TRIGGER refuse_subscription BEFORE INSERT ON garden_seed_watches BEGIN SELECT RAISE(ABORT, 'subscription unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := d.bindDelegationSeed("worker", "planner", "do the work", "worker", seed.ID, "/tmp/a", "codex", false)
	if err == nil || !strings.Contains(err.Error(), "subscription unavailable") {
		t.Fatalf("bind reported success: %v", err)
	}
	dispatch, found := d.gardenDispatch("worker")
	if !found || dispatch.Crown != "" {
		t.Fatalf("failed subscription left a binding: %+v", dispatch)
	}
	if watching, err := d.store.GardenSeedWatching("planner", seed.ID); err != nil || watching {
		t.Fatalf("failed subscription persisted: %v %v", watching, err)
	}
	if _, err := db.Exec(`DROP TRIGGER refuse_subscription`); err != nil {
		t.Fatal(err)
	}
	bound, err := d.bindDelegationSeed("worker", "planner", "do the work", "worker", seed.ID, "/tmp/a", "codex", false)
	if err != nil || bound != seed.ID {
		t.Fatalf("retry = %q %v", bound, err)
	}
	if watching, err := d.store.GardenSeedWatching("planner", seed.ID); err != nil || !watching {
		t.Fatalf("successful binding lacks watch: %v %v", watching, err)
	}
}

func TestSeedHandoverSubscriptionFailureRollsBackTheWholeTransfer(t *testing.T) {
	d, db := persistentSubscriptionGarden(t)
	addGardenSession(t, d, "successor")
	planted := plant(t, d, protocol.SeedPlantMessage{Title: "handover rollback", Body: protocol.Ptr("Continue the implementation")})
	if _, err := d.bindDelegatedSeed("worker", "planner", "work", "worker", planted.ID, "/tmp/a", "codex", false); err != nil {
		t.Fatal(err)
	}
	watchSeed(t, d, "planner", planted.ID, true)
	before, doc, err := d.readSeed(planted.ID)
	if err != nil {
		t.Fatal(err)
	}
	msg := resolvedHandoverRequest(before, doc.Rev, "handover-failure", "planner", "handoff must commit with the transfer")
	if _, err := db.Exec(`CREATE TRIGGER refuse_subscription BEFORE INSERT ON garden_seed_watches BEGIN SELECT RAISE(ABORT, 'subscription unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.bindSeedHandover(msg, "handover-failure", "successor", "/tmp/a", "codex", false); err == nil || !strings.Contains(err.Error(), "subscription unavailable") {
		t.Fatalf("handover failure = %v", err)
	}
	after, afterDoc, err := d.readSeed(planted.ID)
	if err != nil || after.TenderSession != before.TenderSession || after.LastExecutionID != before.LastExecutionID || afterDoc.Rev != doc.Rev {
		t.Fatalf("seed transfer survived rollback: %+v rev=%d err=%v", after, afterDoc.Rev, err)
	}
	old, _ := d.gardenDispatch("worker")
	if old.SupersededBy != "" {
		t.Fatalf("old execution superseded despite rollback: %+v", old)
	}
	if successor, found := d.gardenDispatch("successor"); found {
		t.Fatalf("successor binding survived rollback: %+v", successor)
	}
	notes, total, err := d.readNotes(planted.ID, garden.ShowNotes)
	if err != nil || total != 0 || len(notes) != 0 {
		t.Fatalf("handoff survived rollback: %+v %v", notes, err)
	}
	if _, err := db.Exec(`DROP TRIGGER refuse_subscription`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.bindSeedHandover(msg, "handover-failure", "successor", "/tmp/a", "codex", false); err != nil {
		t.Fatal(err)
	}
	if watching, err := d.store.GardenSeedWatching("successor", planted.ID); err != nil || !watching {
		t.Fatalf("handover retry lacks watch: %v %v", watching, err)
	}
	watchSeed(t, d, "successor", planted.ID, true)
	if _, err := d.bindSeedHandover(msg, "handover-failure", "successor", "/tmp/a", "codex", false); err != nil {
		t.Fatal(err)
	}
	if watching, err := d.store.GardenSeedWatching("successor", planted.ID); err != nil || watching {
		t.Fatalf("binding replay restored watch: %v %v", watching, err)
	}
}

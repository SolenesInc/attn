package daemon

import (
	"database/sql"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestSeedNudges_DispatcherUnwatchStopsPendingAndFutureDescendants(t *testing.T) {
	f := newSeededNudgeGarden(t)
	d := f.d
	if err := d.recordGardenDispatch("sess-c", f.crown.ID, "sess-b", "/tmp/a", "codex", false); err != nil {
		t.Fatal(err)
	}
	ringingNote(t, d, "sess-c", f.leaf.ID, "queued before unwatch", true)
	assertOneSeedBell(t, d, "sess-b", f.leaf.ID, "note")
	result := watchSeed(t, d, "sess-b", f.crown.ID, true)
	if result.Watching || !result.Changed || len(result.WatchingVia) != 0 {
		t.Fatalf("unwatch = %+v", result)
	}
	if bells := queuedSeedBells(t, d, "sess-b"); len(bells) != 0 {
		t.Fatalf("queued descendant survived unwatch: %v", bells)
	}
	ringingNote(t, d, "sess-c", f.leaf.ID, "after unwatch", true)
	future := plant(t, d, protocol.SeedPlantMessage{Title: "future child", PartOf: protocol.Ptr(f.crown.ID)})
	move(t, d, "sess-a", future.ID, garden.VerbTend, "", "")
	if bells := queuedSeedBells(t, d, "sess-b"); len(bells) != 0 {
		t.Fatalf("unwatched dispatcher still rang: %v", bells)
	}
	dispatch, ok := d.gardenDispatch("sess-c")
	if !ok || dispatch.Crown != f.crown.ID || dispatch.DispatcherSession != "sess-b" {
		t.Fatalf("unwatch changed dispatch history: %+v", dispatch)
	}
	watchSeed(t, d, "sess-b", f.crown.ID, false)
	ringingNote(t, d, "sess-a", future.ID, "rewatched", true)
	assertOneSeedBell(t, d, "sess-b", future.ID, "note")
}

func TestSeedNudges_UnwatchPreservesChildSubscriptionsAndOtherRecipients(t *testing.T) {
	for _, delegatedChild := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit child", true: "delegated child"}[delegatedChild], func(t *testing.T) {
			f := newSeededNudgeGarden(t)
			d := f.d
			watchSeed(t, d, "sess-b", f.crown.ID, false)
			watchSeed(t, d, "sess-d", f.crown.ID, false)
			if delegatedChild {
				if err := d.recordGardenDispatch("sess-a", f.child.ID, "sess-b", "/tmp/a", "codex", false); err != nil {
					t.Fatal(err)
				}
			} else {
				watchSeed(t, d, "sess-b", f.child.ID, false)
			}
			if _, err := d.store.EnqueueMaintenancePrompt("unrelated", "sess-b", "keep this maintenance prompt", time.Now()); err != nil {
				t.Fatal(err)
			}
			ringingNote(t, d, "sess-c", f.leaf.ID, "covered by child", true)
			ringingNote(t, d, "sess-c", f.crown.ID, "covered only by plot", true)
			watchSeed(t, d, "sess-b", f.crown.ID, true)
			items, err := d.store.UnreadAgentMailboxDeliveries("sess-b")
			if err != nil || len(items) != 2 {
				t.Fatalf("remaining inbox = %+v, %v", items, err)
			}
			for _, item := range items {
				if item.Item.ID != "unrelated" && item.Item.SourceID != f.leaf.ID {
					t.Fatalf("unexpected surviving update: %+v", item)
				}
			}
			if bells := queuedSeedBells(t, d, "sess-d"); len(bells) != 2 {
				t.Fatalf("other recipient lost updates: %v", bells)
			}
			result := watchSeed(t, d, "sess-b", f.leaf.ID, true)
			if result.Changed || !result.Watching || !reflect.DeepEqual(result.WatchingVia, []string{f.child.ID}) {
				t.Fatalf("inherited unwatch = %+v", result)
			}
			shown := gardenCall(t, func(c net.Conn) {
				d.handleSeedShow(c, &protocol.SeedShowMessage{SeedID: f.leaf.ID, SourceSessionID: protocol.Ptr("sess-b")})
			})
			if !shown.Ok || !reflect.DeepEqual(shown.SeedShowResult.WatchingVia, []string{f.child.ID}) {
				t.Fatalf("inherited show = %+v", shown)
			}
		})
	}
}

func TestSeedNudges_ReplayDoesNotRestoreRemovedWatchButNewBindingDoes(t *testing.T) {
	f := newSeededNudgeGarden(t)
	d := f.d
	if _, err := d.bindDelegatedSeed("sess-c", "sess-b", "work", "worker", f.crown.ID, "/tmp/a", "codex", false); err != nil {
		t.Fatal(err)
	}
	watchSeed(t, d, "sess-b", f.crown.ID, true)
	if _, err := d.bindDelegatedSeed("sess-c", "sess-b", "work", "worker", f.crown.ID, "/tmp/a", "codex", false); err != nil {
		t.Fatal(err)
	}
	if err := d.recordGardenDispatch("sess-c", f.crown.ID, "sess-b", "/tmp/a", "codex", false); err != nil {
		t.Fatal(err)
	}
	if err := d.rememberDispatchResume("sess-c", "native-resume"); err != nil {
		t.Fatal(err)
	}
	if watching, err := d.store.GardenSeedWatching("sess-b", f.crown.ID); err != nil || watching {
		t.Fatalf("replay restored watch: %v %v", watching, err)
	}
	if err := d.recordGardenDispatch("sess-d", f.crown.ID, "sess-b", "/tmp/a", "codex", false); err != nil {
		t.Fatal(err)
	}
	ringingNote(t, d, "sess-c", f.leaf.ID, "fresh delegation", true)
	assertOneSeedBell(t, d, "sess-b", f.leaf.ID, "note")
}

func TestSeedNudges_InboxAndDoorbellDiscardUpdatesAfterRelinking(t *testing.T) {
	for _, doorbell := range []bool{false, true} {
		t.Run(map[bool]string{false: "inbox", true: "doorbell"}[doorbell], func(t *testing.T) {
			f := newSeededNudgeGarden(t)
			d := f.d
			watchSeed(t, d, "sess-b", f.crown.ID, false)
			// Queue directly so the read path is the first delivery attempt.
			if _, err := d.store.ClaimGardenSeedMailboxItem("sess-b", f.leaf.ID, "note", "old-tree", time.Now()); err != nil {
				t.Fatal(err)
			}
			d.noteQueuedAgentMailboxItem("sess-b")
			if resp := link(t, d, f.child.ID, garden.EdgePartOf, f.crown.ID, true); !resp.Ok {
				t.Fatalf("unlink: %+v", resp)
			}
			if doorbell {
				backend := &recordingDoorbell{}
				d.ptyBackend = backend.backend()
				if err := d.deliverAgentMailboxDoorbell("sess-b"); err != nil {
					t.Fatal(err)
				}
				if got := backend.pasted(); len(got) != 0 {
					t.Fatalf("stale doorbell delivered: %v", got)
				}
			} else {
				resp := gardenCall(t, func(c net.Conn) { d.handleAgentInboxBatch(c, "sess-b", 0) })
				if !resp.Ok || len(resp.AgentInboxBatchResult.Items) != 0 || resp.AgentInboxBatchResult.Remaining != 0 {
					t.Fatalf("stale inbox = %+v", resp)
				}
			}
			if got := queuedSeedBells(t, d, "sess-b"); len(got) != 0 {
				t.Fatalf("stale items survived: %v", got)
			}
			if d.hasQueuedAgentMailboxItems("sess-b") {
				t.Fatal("doorbell remains armed with no covered items")
			}
		})
	}
}

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
	if _, err := d.store.ClaimGardenSeedMailboxItem("planner", seed.ID, "note", "pending", time.Now()); err != nil {
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
	msg := handoverRequest(before, doc.Rev, "handover-failure", "planner", "handoff must commit with the transfer")
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
	if watching, err := d.store.GardenSeedWatching("planner", planted.ID); err != nil || !watching {
		t.Fatalf("handover retry lacks watch: %v %v", watching, err)
	}
	watchSeed(t, d, "planner", planted.ID, true)
	if _, err := d.bindSeedHandover(msg, "handover-failure", "successor", "/tmp/a", "codex", false); err != nil {
		t.Fatal(err)
	}
	if watching, err := d.store.GardenSeedWatching("planner", planted.ID); err != nil || watching {
		t.Fatalf("binding replay restored watch: %v %v", watching, err)
	}
}

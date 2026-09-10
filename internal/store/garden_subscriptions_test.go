package store

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
)

func seedSubscriptionHistory(t *testing.T, s *Store) {
	t.Helper()
	now := time.Now()
	for _, schema := range []docstore.CollectionSchema{garden.SeedsSchema(), garden.DispatchesSchema()} {
		if _, err := s.DefineDocumentCollection(schema, now); err != nil {
			t.Fatal(err)
		}
	}
	seedsSchema, _, err := s.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil {
		t.Fatal(err)
	}
	dispatchSchema, _, err := s.DocumentCollection(garden.Namespace, garden.CollectionDispatches)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"plot", "child", "separate"} {
		body, err := (garden.Seed{ID: id, Title: id, Status: garden.StatusPlanted}).Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutDocument(*seedsSchema, id, body, now, nil); err != nil {
			t.Fatal(err)
		}
	}
	dispatches := []garden.Dispatch{
		{SessionID: "old", Crown: "plot", DispatcherSession: "historic", SupersededBy: "current"},
		{SessionID: "duplicate", Crown: "plot", DispatcherSession: "planner"},
		{SessionID: "current", Crown: "plot", DispatcherSession: "planner"},
		{SessionID: "child-worker", Crown: "child", DispatcherSession: "planner"},
		{SessionID: "other-worker", Crown: "plot", DispatcherSession: "other"},
		{SessionID: "missing", Crown: "deleted", DispatcherSession: "planner"},
		{SessionID: "unbound", DispatcherSession: "planner"},
		{SessionID: "no-planner", Crown: "separate"},
	}
	for _, dispatch := range dispatches {
		body, err := dispatch.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutDocument(*dispatchSchema, dispatch.SessionID, body, now, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetGardenSeedWatch("planner", "plot", true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetGardenSeedWatch("explicit", "separate", true, now); err != nil {
		t.Fatal(err)
	}
}

func sortedGardenWatches(t *testing.T, s *Store) []GardenSeedWatch {
	t.Helper()
	watches, err := s.GardenSeedWatches()
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(watches, func(i, j int) bool {
		if watches[i].WatcherSessionID == watches[j].WatcherSessionID {
			return watches[i].SeedID < watches[j].SeedID
		}
		return watches[i].WatcherSessionID < watches[j].WatcherSessionID
	})
	return watches
}

func TestGardenSubscriptionMigrationRunsOnceAndPreservesExplicitWatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garden.db")
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	seedSubscriptionHistory(t, s)
	var created string
	if err := s.db.QueryRow(`SELECT created_at FROM garden_seed_watches WHERE watcher_session_id = 'planner' AND seed_id = 'plot'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 139`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []GardenSeedWatch{{"explicit", "separate"}, {"historic", "plot"}, {"other", "plot"}, {"planner", "child"}, {"planner", "plot"}}
	if got := sortedGardenWatches(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated watches = %+v, want %+v", got, want)
	}
	var preserved string
	if err := s.db.QueryRow(`SELECT created_at FROM garden_seed_watches WHERE watcher_session_id = 'planner' AND seed_id = 'plot'`).Scan(&preserved); err != nil || preserved != created {
		t.Fatalf("explicit watch timestamp = %q, want %q: %v", preserved, created, err)
	}
	if _, err := s.SetGardenSeedWatch("planner", "plot", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if watching, err := s.GardenSeedWatching("planner", "plot"); err != nil || watching {
		t.Fatalf("restart resurrected subscription: %v %v", watching, err)
	}
}

func TestGardenSubscriptionMigrationFailureRollsBackAndRetries(t *testing.T) {
	s := newAgentMailboxStore(t)
	seedSubscriptionHistory(t, s)
	before := sortedGardenWatches(t, s)
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 139;
  CREATE TRIGGER refuse_migrated_watch BEFORE INSERT ON garden_seed_watches
  WHEN NEW.watcher_session_id = 'other'
  BEGIN SELECT RAISE(ABORT, 'subscription disk failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(s.db, ""); err == nil || !strings.Contains(err.Error(), "subscription disk failure") {
		t.Fatalf("migration failure = %v", err)
	}
	if got := sortedGardenWatches(t, s); !reflect.DeepEqual(got, before) {
		t.Fatalf("partial migration: %+v, before %+v", got, before)
	}
	var receipt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 139`).Scan(&receipt); err != nil || receipt != 0 {
		t.Fatalf("failed migration receipt = %d: %v", receipt, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER refuse_migrated_watch`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(s.db, ""); err != nil {
		t.Fatal(err)
	}
	if got := sortedGardenWatches(t, s); len(got) != 5 {
		t.Fatalf("retry watches = %+v", got)
	}
}

func TestGardenDispatchCommitRollsBackBindingAndFactsWhenWatchFails(t *testing.T) {
	s := newAgentMailboxStore(t)
	seedSubscriptionHistory(t, s)
	schema, _, err := s.DocumentCollection(garden.Namespace, garden.CollectionDispatches)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := (garden.Dispatch{SessionID: "fresh", Crown: "child", DispatcherSession: "new-planner"}).Encode()
	absent := docstore.ExpectAbsent
	commits := []DocumentCommit{{Write: DocumentWrite{Schema: *schema, ID: "fresh", Body: body, Expected: &absent}, Fact: BusEvent{Name: "document.changed", Subject: "garden/dispatches/fresh"}}}
	if _, err := s.db.Exec(`CREATE TRIGGER refuse_new_watch BEFORE INSERT ON garden_seed_watches
  BEGIN SELECT RAISE(ABORT, 'watch refused'); END`); err != nil {
		t.Fatal(err)
	}
	watch := GardenSeedWatch{WatcherSessionID: "new-planner", SeedID: "child"}
	if _, err := s.CommitGardenDispatchWrites(commits, watch, time.Now()); err == nil || !strings.Contains(err.Error(), "watch refused") {
		t.Fatalf("commit error = %v", err)
	}
	if _, found, err := s.GetDocument(*schema, "fresh"); err != nil || found {
		t.Fatalf("binding survived: %v %v", found, err)
	}
	if events := factsOnLog(t, s); len(events) != 0 {
		t.Fatalf("facts survived rollback: %+v", events)
	}
	if _, err := s.db.Exec(`DROP TRIGGER refuse_new_watch`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitGardenDispatchWrites(commits, watch, time.Now()); err != nil {
		t.Fatal(err)
	}
	if watching, err := s.GardenSeedWatching("new-planner", "child"); err != nil || !watching {
		t.Fatalf("watch missing after retry: %v %v", watching, err)
	}
}

func TestDiscardGardenUpdatesPreservesOtherRecipientsAndPeerMessages(t *testing.T) {
	s := newAgentMailboxStore(t)
	now := time.Now()
	enqueuePeer(t, s, "peer", "sender", "planner", "keep this message", now)
	for _, item := range []struct{ session, seed, id string }{{"planner", "plot", "a"}, {"planner", "child", "b"}, {"other", "plot", "c"}} {
		if _, err := s.ClaimGardenSeedMailboxItem(item.session, item.seed, "note", item.id, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DiscardGardenSeedMailboxItems("planner", []string{"plot"}, now); err != nil {
		t.Fatal(err)
	}
	items, err := s.UnreadAgentMailboxDeliveries("planner")
	if err != nil || len(items) != 2 || items[0].Item.ID != "b" || items[1].Item.ID != "peer" {
		t.Fatalf("planner inbox = %+v, %v", items, err)
	}
	other, err := s.UnreadGardenSeedMailboxSeeds("other")
	if err != nil || !reflect.DeepEqual(other, []string{"plot"}) {
		t.Fatalf("other inbox = %v %v", other, err)
	}
}

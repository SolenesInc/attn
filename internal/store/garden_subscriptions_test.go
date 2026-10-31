package store

import (
	"path/filepath"
	"reflect"
	"slices"
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
		if _, err := defineLegacyDocumentCollection(s, schema, now); err != nil {
			t.Fatal(err)
		}
	}
	seedsSchema, _, err := legacyDocumentCollection(s, garden.Namespace, garden.CollectionSeeds)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.ProfileMigration()
	if err != nil {
		t.Fatal(err)
	}
	dispatchSchema, _, err := legacyDocumentCollection(s, garden.Namespace, garden.CollectionDispatches)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"plot", "child", "separate"} {
		body, err := (garden.Seed{ID: id, ProfileID: profile.Manifest.ProfileID, Title: id, Status: garden.StatusPlanted}).Encode()
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
		if _, err := s.PutDocument(*dispatchSchema, string(dispatch.SessionID), body, now, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, watch := range []GardenSeedWatch{{"planner", "plot"}, {"explicit", "separate"}} {
		legacySeedWatch(t, s, watch, true, now)
	}
}

func legacySeedWatch(t *testing.T, s *Store, watch GardenSeedWatch, watching bool, now time.Time) {
	t.Helper()
	query, args := `INSERT OR IGNORE INTO garden_seed_watches(watcher_session_id, seed_id, created_at) VALUES (?, ?, ?)`, []any{watch.WatcherSessionID, watch.SeedID, now.UTC().Format(sortableTimeFormat)}
	if !watching {
		query, args = `DELETE FROM garden_seed_watches WHERE watcher_session_id = ? AND seed_id = ?`, []any{watch.WatcherSessionID, watch.SeedID}
	}
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func sortedGardenWatches(t *testing.T, s *Store) []GardenSeedWatch {
	t.Helper()
	rows, err := s.db.Query(`SELECT watcher_session_id, seed_id FROM garden_seed_watches`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var watches []GardenSeedWatch
	for rows.Next() {
		var watch GardenSeedWatch
		if err := rows.Scan(&watch.WatcherSessionID, &watch.SeedID); err != nil {
			t.Fatal(err)
		}
		watches = append(watches, watch)
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
	s, err := newStoreAtVersion(path, 167)
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
	s, err = newStoreAtVersion(path, 167)
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
	legacySeedWatch(t, s, GardenSeedWatch{"planner", "plot"}, false, time.Now())
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = newStoreAtVersion(path, 167)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := sortedGardenWatches(t, s); slices.Contains(got, GardenSeedWatch{"planner", "plot"}) {
		t.Fatalf("restart resurrected subscription: %+v", got)
	}
}

func TestGardenSubscriptionMigrationFailureRollsBackAndRetries(t *testing.T) {
	s := migrationFixtureStore(t, 167)
	seedSubscriptionHistory(t, s)
	before := sortedGardenWatches(t, s)

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 139;
  CREATE TRIGGER refuse_migrated_watch BEFORE INSERT ON garden_seed_watches
  WHEN NEW.watcher_session_id = 'other'
  BEGIN SELECT RAISE(ABORT, 'subscription disk failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDBThrough(s.db, "", 167); err == nil || !strings.Contains(err.Error(), "subscription disk failure") {
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
	if err := migrateDBThrough(s.db, "", 167); err != nil {
		t.Fatal(err)
	}
	if got := sortedGardenWatches(t, s); len(got) != 5 {
		t.Fatalf("retry watches = %+v", got)
	}
}

package store

import (
	"testing"
	"time"
)

var busBase = time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

func TestGardenSeedArtifactObservationDeduplicatesOnlyTheCurrentSnapshot(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	event := BusEvent{Name: "garden.seed.artifact.changed", Subject: "s-seed01", Payload: `{}`, Source: "garden"}

	first, changed, err := s.AppendGardenSeedArtifactObservation("a", event, busBase)
	if err != nil || !changed {
		t.Fatalf("first observation: seq=%d changed=%v err=%v", first, changed, err)
	}
	same, changed, err := s.AppendGardenSeedArtifactObservation("a", event, busBase.Add(time.Minute))
	if err != nil || changed || same != first {
		t.Fatalf("repeated current observation: seq=%d changed=%v err=%v, want seq=%d unchanged", same, changed, err, first)
	}
	second, changed, err := s.AppendGardenSeedArtifactObservation("b", event, busBase.Add(2*time.Minute))
	if err != nil || !changed || second <= first {
		t.Fatalf("second observation: seq=%d changed=%v err=%v, want after %d", second, changed, err, first)
	}
	third, changed, err := s.AppendGardenSeedArtifactObservation("a", event, busBase.Add(3*time.Minute))
	if err != nil || !changed || third <= second {
		t.Fatalf("return to first snapshot: seq=%d changed=%v err=%v, want after %d", third, changed, err, second)
	}
}

func TestGardenSeedArtifactTransferObservationDeduplicatesTheWatcher(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	event := BusEvent{Name: "garden.seed.artifact.changed", Subject: "s-seed01", Payload: `{}`, Source: "garden"}

	transfer, inserted, err := s.AppendGardenSeedArtifactTransferObservation(
		"transfer-a", "", "snapshot-a", event, busBase,
	)
	if err != nil || !inserted || transfer == 0 {
		t.Fatalf("transfer observation: seq=%d inserted=%v err=%v", transfer, inserted, err)
	}
	retry, inserted, err := s.AppendGardenSeedArtifactTransferObservation(
		"transfer-a", "", "snapshot-a", event, busBase.Add(time.Minute),
	)
	if err != nil || inserted || retry != transfer {
		t.Fatalf("transfer retry: seq=%d inserted=%v err=%v, want existing %d", retry, inserted, err, transfer)
	}
	observed, changed, err := s.AppendGardenSeedArtifactObservation("snapshot-a", event, busBase.Add(2*time.Minute))
	if err != nil || changed || observed != transfer {
		t.Fatalf("watcher observation: seq=%d changed=%v err=%v, want existing %d", observed, changed, err, transfer)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER refuse_transfer_observation BEFORE UPDATE ON garden_seed_artifact_observations
		BEGIN SELECT RAISE(ABORT, 'observation failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendGardenSeedArtifactTransferObservation(
		"transfer-b", "transfer-a", "snapshot-b", event, busBase.Add(3*time.Minute),
	); err == nil {
		t.Fatal("transfer event survived a failed observation update")
	}
	var events, oldSources, newSources int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM bus_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM garden_seed_event_sources WHERE source_id='transfer-a'`).Scan(&oldSources); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM garden_seed_event_sources WHERE source_id='transfer-b'`).Scan(&newSources); err != nil {
		t.Fatal(err)
	}
	if events != 1 || oldSources != 1 || newSources != 0 {
		t.Fatalf("failed atomic transfer left events=%d old_sources=%d new_sources=%d, want 1, 1, 0", events, oldSources, newSources)
	}
}

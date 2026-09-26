package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func newTurnStore(t *testing.T) *Store {
	t.Helper()
	s, err := newSeededStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func addTurnSession(t *testing.T, s *Store, id string, state protocol.SessionState) {
	t.Helper()
	now := time.Now().Format(time.RFC3339Nano)
	if err := s.AddChecked(&protocol.Session{
		ID:             id,
		Label:          id,
		Directory:      "/tmp/" + id,
		WorkspaceID:    "ws-1",
		State:          state,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	}); err != nil {
		t.Fatalf("add session %s: %v", id, err)
	}
}

func TestMigration81BackfillsOpenTurnsFromStateSince(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-81.db")
	db, err := openSeededDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB setup: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO sessions (id, label, directory, state, state_since, state_updated_at, last_seen) VALUES
			('waiting',  'Waiting',  '/tmp/a', 'waiting_input',    '2026-07-26T10:00:00Z', '2026-07-26T10:00:00Z', '2026-07-26T10:00:00Z'),
			('approval', 'Approval', '/tmp/b', 'pending_approval', '2026-07-26T11:00:00Z', '2026-07-26T11:00:00Z', '2026-07-26T11:00:00Z'),
			('unknown',  'Unknown',  '/tmp/c', 'unknown',          '2026-07-26T12:00:00Z', '2026-07-26T12:00:00Z', '2026-07-26T12:00:00Z'),
			('working',  'Working',  '/tmp/d', 'working',          '2026-07-26T13:00:00Z', '2026-07-26T13:00:00Z', '2026-07-26T13:00:00Z'),
			('idle',     'Idle',     '/tmp/e', 'idle',             '2026-07-26T14:00:00Z', '2026-07-26T14:00:00Z', '2026-07-26T14:00:00Z');
		ALTER TABLE sessions DROP COLUMN turn_opened_at;
		ALTER TABLE sessions DROP COLUMN turn_settled_at;
		DELETE FROM schema_migrations WHERE version >= 81;
	`); err != nil {
		t.Fatalf("seed pre-81 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-81 database: %v", err)
	}

	migrated, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer migrated.Close()

	for id, want := range map[string]string{
		"waiting":  "2026-07-26T10:00:00Z",
		"approval": "2026-07-26T11:00:00Z",
		"unknown":  "2026-07-26T12:00:00Z",
	} {
		got := migrated.TurnStamps(id).OpenedAt
		if got.IsZero() {
			t.Errorf("%s: no turn opened by the backfill", id)
			continue
		}
		if got.Format(time.RFC3339) != want {
			t.Errorf("%s: opened_at = %s, want %s (the age it has been waiting)", id, got.Format(time.RFC3339), want)
		}
	}
	for _, id := range []string{"working", "idle"} {
		if !migrated.TurnStamps(id).OpenedAt.IsZero() {
			t.Errorf("%s: backfill opened a turn for a state that does not open one", id)
		}
	}
}

func TestAStateOpensItsTurnOnlyThroughTheSnoozeItFindsWhenItCommits(t *testing.T) {
	stores := map[string]func(t *testing.T) *Store{
		"sqlite": newTurnStore,
		"memory": func(*testing.T) *Store { return New() },
	}
	tests := []struct {
		name         string
		snoozeFor    time.Duration
		opening      TurnOpening
		wantOpen     bool
		wantSnoozed  bool
		wantHeld     bool
		wantEndsWake bool
	}{
		{"no snooze", 0, TurnOpening{Opens: true}, true, false, false, false},
		{"a snooze that is still running", time.Hour, TurnOpening{Opens: true}, false, true, true, false},
		{"a snooze the state breaks", time.Hour, TurnOpening{Opens: true, BreaksSnooze: true}, true, false, false, true},
		{"a snooze that already expired", -time.Minute, TurnOpening{Opens: true}, true, false, false, true},
		{"a state that opens no turn", time.Hour, TurnOpening{}, false, true, false, false},
	}
	for backend, open := range stores {
		for _, tt := range tests {
			t.Run(backend+"/"+tt.name, func(t *testing.T) {
				s := open(t)
				addTurnSession(t, s, "s1", protocol.SessionStateWorking)
				until := time.Now().Add(tt.snoozeFor).UTC()
				if tt.snoozeFor != 0 && !s.SnoozeTurn("s1", until, time.Now().Add(-2*time.Hour)) {
					t.Fatal("snooze the session")
				}

				applied, outcome := s.UpdateStateOpeningTurn("s1", string(protocol.SessionStateIdle), tt.opening)

				if !applied {
					t.Fatal("the state was not committed")
				}
				if got := s.Get("s1").State; got != protocol.SessionStateIdle {
					t.Errorf("state = %s, want idle", got)
				}
				stamps := s.TurnStamps("s1")
				if open := stamps.OpenedAt.After(stamps.SettledAt); open != tt.wantOpen {
					t.Errorf("turn open = %v, want %v (stamps %+v)", open, tt.wantOpen, stamps)
				}
				if snoozed := !stamps.SnoozedUntil.IsZero(); snoozed != tt.wantSnoozed {
					t.Errorf("still snoozed = %v, want %v", snoozed, tt.wantSnoozed)
				}
				if outcome.HeldBySnooze != tt.wantHeld {
					t.Errorf("held by snooze = %v, want %v", outcome.HeldBySnooze, tt.wantHeld)
				}
				if ended := !outcome.EndedSnooze.IsZero(); ended != tt.wantEndsWake || ended && !outcome.EndedSnooze.Equal(until) {
					t.Errorf("ended snooze = %s, want the deadline %s ended: %v", outcome.EndedSnooze, until, tt.wantEndsWake)
				}
			})
		}
	}
}

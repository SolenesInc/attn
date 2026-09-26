package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func closeAt(t *testing.T, s *Store, id string, closed SessionClose, at time.Time) {
	t.Helper()
	recorded, err := s.CloseSession(id, closed, at)
	if err != nil {
		t.Fatalf("close %s: %v", id, err)
	}
	if !recorded {
		t.Fatalf("close %s recorded nothing, want the row marked closed", id)
	}
}

func TestALiftedCloseGoesBackExactly(t *testing.T) {
	backings := map[string]func(*testing.T) *Store{
		"sqlite": newTurnStore,
		"maps":   func(*testing.T) *Store { return newMapBackedStore() },
	}
	for name, newStore := range backings {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			addTurnSession(t, s, "s1", protocol.SessionStateIdle)
			closeAt(t, s, "s1", SessionClose{By: "sess-dispatcher", Reason: "brief delivered"},
				time.Date(2026, 3, 4, 5, 6, 7, 89, time.UTC))
			closed := s.SessionLedgerEntry("s1")
			if closed == nil || protocol.Deref(closed.ClosedAt) == "" {
				t.Fatalf("ledger entry after the close = %+v, want it closed", closed)
			}

			lifted, reopened, err := s.ReopenSession("s1")
			if err != nil || !reopened {
				t.Fatalf("ReopenSession = %v, %v, want the close lifted", reopened, err)
			}
			if s.Get("s1") == nil {
				t.Fatal("Get(s1) = nil after reopening, want the session live again")
			}

			restored, err := s.RestoreSessionClose("s1", lifted)
			if err != nil || !restored {
				t.Fatalf("RestoreSessionClose = %v, %v, want the close back", restored, err)
			}
			if session := s.Get("s1"); session != nil {
				t.Errorf("Get(s1) = %+v after restoring the close, want it hidden again", session)
			}
			if got := s.SessionLedgerEntry("s1"); !reflect.DeepEqual(got, closed) {
				t.Errorf("ledger entry after reopen and restore:\n got=%+v\nwant=%+v", got, closed)
			}
		})
	}
}

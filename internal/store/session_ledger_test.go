package store

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func closeAt(t *testing.T, s *Store, id string, closed SessionClose, at time.Time) {
	t.Helper()
	recorded, err := s.CloseSession(protocol.SessionID(id), closed, at)
	if err != nil {
		t.Fatalf("close %s: %v", id, err)
	}
	if !recorded {
		t.Fatalf("close %s recorded nothing, want the row marked closed", id)
	}
}

func ledgerIDs(page SessionLedgerPage) []string {
	ids := make([]string, 0, len(page.Entries))
	for _, entry := range page.Entries {
		ids = append(ids, string(entry.ID))
	}
	return ids
}

func addLedgerSession(t *testing.T, s *Store, id, profileID, repository string, lastSeen time.Time) {
	t.Helper()
	s.Add(&protocol.Session{
		ID:         protocol.SessionID(id),
		Label:      id,
		Directory:  "/tmp/" + id,
		ProfileID:  profileID,
		Repository: protocol.Ptr(repository),
		State:      protocol.SessionStateIdle,
		StateSince: protocol.NewTimestamp(lastSeen).String(),
		LastSeen:   protocol.NewTimestamp(lastSeen).String(),
	})
}

func ledgerProfileID(t *testing.T, s *Store, name string) string {
	t.Helper()
	if s.db == nil {
		return "profile-" + name
	}
	profile, _ := mustCreateProfile(t, s, name)
	return profile.ID
}

func ledgerBackings() map[string]func(*testing.T) *Store {
	return map[string]func(*testing.T) *Store{
		"sqlite": newLedgerStore,
		"maps":   func(*testing.T) *Store { return newMapBackedStore() },
	}
}

func TestSessionLedgerFiltersByProfileAndRepository(t *testing.T) {
	for name, newStore := range ledgerBackings() {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
			attn, side := ledgerProfileID(t, s, "attn"), ledgerProfileID(t, s, "side")
			addLedgerSession(t, s, "attn-one", attn, "/repos/attn", at)
			addLedgerSession(t, s, "attn-two", side, "/repos/attn", at.Add(time.Minute))
			addLedgerSession(t, s, "other", attn, "/repos/other", at.Add(2*time.Minute))

			byRepo, err := s.SessionLedger(SessionLedgerQuery{Scope: SessionLedgerAll, Repository: "/repos/attn"})
			if err != nil {
				t.Fatalf("repository page: %v", err)
			}
			if got := ledgerIDs(byRepo); len(got) != 2 || !slices.Contains(got, "attn-one") || !slices.Contains(got, "attn-two") {
				t.Errorf("repository page = %v, want both attn rows", got)
			}

			byProfile, err := s.SessionLedger(SessionLedgerQuery{Scope: SessionLedgerAll, ProfileID: attn})
			if err != nil {
				t.Fatalf("profile page: %v", err)
			}
			if got := ledgerIDs(byProfile); len(got) != 2 || !slices.Contains(got, "attn-one") || !slices.Contains(got, "other") {
				t.Errorf("profile page = %v, want both rows of the attn profile", got)
			}

			both, err := s.SessionLedger(SessionLedgerQuery{
				Scope: SessionLedgerAll, ProfileID: attn, Repository: "/repos/attn",
			})
			if err != nil {
				t.Fatalf("combined page: %v", err)
			}
			if got := ledgerIDs(both); len(got) != 1 || got[0] != "attn-one" {
				t.Errorf("combined page = %v, want the one row matching both", got)
			}
		})
	}
}

func TestTheLedgerNamesEachRowsProfileAndKeepsItAfterTheProfileIsDeleted(t *testing.T) {
	s, _ := openProfileStore(t)
	work, _ := mustCreateProfile(t, s, "Work")
	home, _ := mustCreateProfile(t, s, "Home")
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	addLedgerSession(t, s, "closed-in-work", work.ID, "/repos/attn", at)
	addLedgerSession(t, s, "live-in-work", work.ID, "/repos/attn", at.Add(time.Minute))
	addLedgerSession(t, s, "live-in-home", home.ID, "/repos/attn", at.Add(2*time.Minute))
	closeAt(t, s, "closed-in-work", SessionClose{By: SessionClosedByUser}, at.Add(3*time.Minute))
	closeAt(t, s, "live-in-work", SessionClose{By: SessionClosedByUser}, at.Add(3*time.Minute))

	if _, err := s.DeleteProfile(work.ID, work.Revision, 0, 0); err != nil {
		t.Fatal(err)
	}

	page, err := s.SessionLedger(SessionLedgerQuery{Scope: SessionLedgerAll, Facets: true})
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]protocol.SessionLedgerEntry{}
	for _, entry := range page.Entries {
		rows[string(entry.ID)] = entry
	}
	if closed := rows["closed-in-work"]; closed.ProfileID != work.ID || closed.ProfileName != "Work" || !protocol.Deref(closed.ProfileDeleted) {
		t.Errorf("closed row = %s %q deleted=%v, want its deleted profile Work kept as history", closed.ProfileID, closed.ProfileName, closed.ProfileDeleted)
	}
	if moved := rows["live-in-work"]; moved.ProfileID != work.ID || moved.ProfileName != "Work" || !protocol.Deref(moved.ProfileDeleted) {
		t.Errorf("live row = %s %q deleted=%v, want its original deleted profile Work", moved.ProfileID, moved.ProfileName, moved.ProfileDeleted)
	}
	want := []protocol.SessionLedgerProfileFacet{
		{ProfileID: home.ID, Name: "Home", Count: 1},
		{ProfileID: work.ID, Name: "Work", Deleted: protocol.Ptr(true), Count: 2},
	}
	if !reflect.DeepEqual(page.Facets.Profiles, want) {
		t.Errorf("profile facets = %+v, want %+v", page.Facets.Profiles, want)
	}

	history, err := s.SessionLedger(SessionLedgerQuery{Scope: SessionLedgerAll, ProfileID: work.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := ledgerIDs(history); !slices.Equal(got, []string{"live-in-work", "closed-in-work"}) {
		t.Errorf("filtering by the deleted profile = %v, want its closed history", got)
	}
	if shown := s.SessionLedgerEntry("closed-in-work"); shown == nil || shown.ProfileName != "Work" || !protocol.Deref(shown.ProfileDeleted) {
		t.Errorf("show = %+v, want the deleted profile's name and flag", shown)
	}
}

func newLedgerStore(t *testing.T) *Store {
	t.Helper()
	s, err := newSeededStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

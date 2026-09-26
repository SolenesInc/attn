package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newRecentLocationsStore(t *testing.T) *Store {
	t.Helper()
	s, err := newSeededStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedRecentLocation(t *testing.T, s *Store, path, lastSeen string, useCount int) {
	t.Helper()
	_, err := s.db.Exec(
		"INSERT INTO recent_locations (path, last_seen, use_count) VALUES (?, ?, ?)",
		path, lastSeen, useCount,
	)
	if err != nil {
		t.Fatalf("failed to seed recent location: %v", err)
	}
}

func TestGetRecentLocationsRanksByFrecency(t *testing.T) {
	s := newRecentLocationsStore(t)
	now := time.Now()
	frequentOld := t.TempDir()
	recentOnce := t.TempDir()
	staleOnce := t.TempDir()

	seedRecentLocation(t, s, frequentOld, now.Add(-72*time.Hour).Format(time.RFC3339), 10)
	seedRecentLocation(t, s, recentOnce, now.Format(time.RFC3339), 1)
	seedRecentLocation(t, s, staleOnce, now.Add(-30*24*time.Hour).Format(time.RFC3339), 1)

	locs := s.GetRecentLocations(10)
	if len(locs) != 3 {
		t.Fatalf("expected 3 locations, got %d", len(locs))
	}
	want := []string{frequentOld, recentOnce, staleOnce}
	for i, path := range want {
		if locs[i].Path != path {
			t.Errorf("position %d: expected %s, got %s", i, path, locs[i].Path)
		}
	}
}

func TestGetRecentLocationsRanksBeforeTruncating(t *testing.T) {
	s := newRecentLocationsStore(t)
	root := t.TempDir()
	now := time.Now()

	frequentOld := filepath.Join(root, "frequent-old")
	if err := os.MkdirAll(frequentOld, 0o755); err != nil {
		t.Fatal(err)
	}
	seedRecentLocation(t, s, frequentOld, now.Add(-30*24*time.Hour).Format(time.RFC3339), 100)

	for i := 0; i < 250; i++ {
		dir := filepath.Join(root, fmt.Sprintf("fresh-%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		seedRecentLocation(t, s, dir, now.Format(time.RFC3339), 1)
	}

	locs := s.GetRecentLocations(10)
	if len(locs) != 10 {
		t.Fatalf("expected 10 locations, got %d", len(locs))
	}
	if locs[0].Path != frequentOld {
		t.Errorf("expected %s first, got %s", frequentOld, locs[0].Path)
	}
}

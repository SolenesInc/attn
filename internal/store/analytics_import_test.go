package store

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/analytics"
)

func TestARetriedForwardStoresEachRecordOnceAndListsUnderItsStream(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	batch := []string{`{"stream":"bus_m9c1e04b7aa21","seq":7,"name":"session.registered","at":"2026-11-06T10:00:00Z"}`}
	for range 2 {
		if err := s.ImportAnalyticsRecords(analytics.Build{Version: "1"}, batch); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.AnalyticsRecords("", "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Rows) != 1 {
		t.Fatalf("the retried batch stored %d rows, want 1", len(all.Rows))
	}
	bus, err := s.AnalyticsRecords("", analytics.StreamBus, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bus.Rows) != 1 {
		t.Fatalf("the bus filter finds %d of an outpost's bus records, want 1", len(bus.Rows))
	}
	cli, err := s.AnalyticsRecords("", analytics.StreamCLI, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cli.Rows) != 0 {
		t.Fatalf("the cli filter finds %d of an outpost's bus records, want 0", len(cli.Rows))
	}
}

package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

func TestARetriedForwardStoresEachRecordOnceAndListsUnderItsStream(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetSettingChecked(setting.AnalyticsSubjectSecret.Key, "aa"); err != nil {
		t.Fatal(err)
	}
	unsent, err := s.UnsentAnalyticsRecords(1, analytics.Build{}, "")
	if err != nil {
		t.Fatal(err)
	}
	generation := unsent.Generation
	batch := []string{`{"stream":"bus_m9c1e04b7aa21","seq":7,"name":"session.registered","at":"2026-11-06T10:00:00Z"}`}
	for range 2 {
		if err := s.ImportAnalyticsRecords(analytics.Build{Version: "1"}, batch, generation); err != nil {
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

func TestACopiedGenerationDiscardsTheOutboxAtomicallyAndAnUnchangedOneKeepsIt(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	copySetting := func(key, value string) {
		t.Helper()
		row := protocol.CopyRow{Table: protocol.CopyTableSettings, Key: key, Columns: protocol.RecordUnknown{"key": key, "value": value}}
		if err := s.ApplyCopy(context.Background(), "home", []protocol.CopyRow{row}); err != nil {
			t.Fatal(err)
		}
	}
	held := func() (int, string) {
		t.Helper()
		unsent, err := s.UnsentAnalyticsRecords(10, analytics.Build{}, "home")
		if err != nil {
			t.Fatal(err)
		}
		return len(unsent.Rows), unsent.Generation
	}
	record := func(secret byte) {
		t.Helper()
		row := analytics.Record{Stream: analytics.StreamCLI, Name: "settings get"}
		if err := s.AppendAnalyticsStreamRecord(row, analytics.Build{}, []byte{secret}, "home"); err != nil {
			t.Fatal(err)
		}
	}
	copySetting(setting.AnalyticsSubjectSecret.Key, "aa")
	record(0xaa)
	rows, first := held()
	if rows != 1 {
		t.Fatalf("holds %d rows, want the 1 recorded", rows)
	}

	copySetting(setting.AnalyticsSubjectSecret.Key, "aa")
	copySetting(setting.AnalyticsClearedAt.Key, "")
	if rows, generation := held(); rows != 1 || generation != first {
		t.Fatalf("a repeated copy on reconnect left %d rows under %s, want 1 under %s", rows, generation, first)
	}

	copySetting(setting.AnalyticsClearedAt.Key, "t1")
	if rows, generation := held(); rows != 0 || generation == first {
		t.Fatalf("a new clear stamp left %d rows under %s, was %s", rows, generation, first)
	}

	record(0xaa)
	copySetting(setting.AnalyticsSubjectSecret.Key, "bb")
	if rows, _ := held(); rows != 0 {
		t.Fatalf("a new secret left %d rows hashed with the old one", rows)
	}

	record(0xaa)
	if rows, _ := held(); rows != 0 {
		t.Fatalf("a record hashed with the replaced secret was kept, %d rows", rows)
	}
}

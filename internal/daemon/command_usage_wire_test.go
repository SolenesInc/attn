package daemon_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func recordUsage(app *testworld.Peer, profile, command string) protocol.RecordCommandUsageResultMessage {
	id := uuid.NewString()
	return testworld.Request(app, protocol.RecordCommandUsageMessage{Cmd: protocol.CmdRecordCommandUsage, RequestID: id, ProfileID: profile, CommandID: command},
		protocol.EventRecordCommandUsageResult, func(r protocol.RecordCommandUsageResultMessage) bool { return r.RequestID == id })
}

func readUsage(app *testworld.Peer, profile string) protocol.GetCommandUsageResultMessage {
	id := uuid.NewString()
	return testworld.Request(app, protocol.GetCommandUsageMessage{Cmd: protocol.CmdGetCommandUsage, RequestID: id, ProfileID: profile},
		protocol.EventGetCommandUsageResult, func(r protocol.GetCommandUsageResultMessage) bool { return r.RequestID == id })
}

func TestCommandUsageDecayAndProfileScope(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		home := app.SelectedProfile()
		if r := readUsage(app, home); !r.Success || len(r.Entries) != 0 {
			t.Fatalf("cold history: %+v", r)
		}
		for range 8 {
			if r := recordUsage(app, home, "old"); !r.Success {
				t.Fatalf("record: %+v", r)
			}
		}
		w.stop()
		w.advance(42 * 24 * time.Hour)
		w.restart()
		app = w.AppOn(home)
		if r := recordUsage(app, home, "new"); !r.Success {
			t.Fatalf("record: %+v", r)
		}
		entries := readUsage(app, home).Entries
		if len(entries) != 2 || entries[0].CommandID != "new" || entries[1].CommandID != "old" || entries[0].Score != 1 || entries[1].Score != 1 || entries[0].LastUsedAt <= entries[1].LastUsedAt {
			t.Fatalf("42-day tie and recency: %+v", entries)
		}
		recordUsage(app, home, "old")
		w.stop()
		w.advance(14 * 24 * time.Hour)
		w.restart()
		app = w.AppOn(home)
		entries = readUsage(app, home).Entries
		if entries[0].Score != 0.5 || entries[1].Score != 1 {
			t.Fatalf("new selection revives only decayed score: %+v", entries)
		}
		side := createProfile(app, "Side")
		if r := recordUsage(app, side.ID, "foreign"); r.Success {
			t.Fatal("mismatched profile accepted")
		}
		selectProfile(app, side.ID)
		if r := readUsage(app, side.ID); !r.Success || len(r.Entries) != 0 {
			t.Fatalf("profile isolation: %+v", r)
		}
		if r := recordUsage(app, home, "foreign"); r.Success {
			t.Fatal("old profile accepted after switch")
		}
		if r := recordUsage(app, side.ID, "own"); !r.Success {
			t.Fatalf("side record: %+v", r)
		}
		selectProfile(app, home)
		if r := readUsage(app, home); len(r.Entries) != 2 {
			t.Fatalf("source profile history: %+v", r)
		}
	})
}

func TestCommandUsageConcurrentWindowsAndRestart(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		first := w.App()
		profile := first.SelectedProfile()
		second := w.AppOn(profile)
		var wg sync.WaitGroup
		for _, app := range []*testworld.Peer{first, second} {
			wg.Go(func() {
				for range 8 {
					if r := recordUsage(app, profile, "shared"); !r.Success {
						t.Errorf("record: %+v", r)
					}
				}
			})
		}
		wg.Wait()
		r := readUsage(first, profile)
		if !r.Success || len(r.Entries) != 1 || r.Entries[0].Score != 16 {
			t.Fatalf("concurrent increments: %+v", r)
		}
		stamp, score := r.Entries[0].LastUsedAt, r.Entries[0].Score
		w.restart()
		r = readUsage(w.AppOn(profile), profile)
		if !r.Success || len(r.Entries) != 1 || r.Entries[0].LastUsedAt != stamp || r.Entries[0].Score != score {
			t.Fatalf("restart history: %+v", r)
		}
	})
}

func TestCommandUsageSelectionBeforeProfileSwitch(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		home := app.SelectedProfile()
		side := createProfile(app, "Side")
		id := uuid.NewString()
		app.Send(protocol.RecordCommandUsageMessage{Cmd: protocol.CmdRecordCommandUsage, RequestID: id, ProfileID: home, CommandID: "switch-profile:" + side.ID})
		selectProfile(app, side.ID)
		r := testworld.Await(app, protocol.EventRecordCommandUsageResult, func(r protocol.RecordCommandUsageResultMessage) bool { return r.RequestID == id })
		if !r.Success {
			t.Fatalf("source selection: %+v", r)
		}
		if r := readUsage(app, side.ID); len(r.Entries) != 0 {
			t.Fatalf("target history: %+v", r)
		}
		selectProfile(app, home)
		if r := readUsage(app, home); len(r.Entries) != 1 {
			t.Fatalf("source history: %+v", r)
		}
	})
}

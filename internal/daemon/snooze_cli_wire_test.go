package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSnoozeSocketRepliesAndLedgerVisibility(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "self", "other")
		snoozeReport(t, w, cli, "self", protocol.StateWaitingInput)
		until := time.Now().Add(time.Hour)
		if err := cli.SnoozeSession("self", "self", until); err != nil {
			t.Fatal(err)
		}
		held := testworld.AwaitSession(app, "self", func(s protocol.Session) bool { return snoozedUntil(s).Equal(until) })
		if protocol.Deref(held.TurnOwed) {
			t.Fatal("snoozed session still owes a turn")
		}
		shown, err := cli.SessionShow("self")
		if err != nil || protocol.Deref(shown.Entry.TurnSnoozedUntil) != until.UTC().Format(time.RFC3339Nano) {
			t.Fatalf("ledger show: %+v %v", shown, err)
		}
		page, err := cli.SessionList(client.SessionListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range page.Entries {
			if entry.ID == "self" {
				found = protocol.Deref(entry.TurnSnoozedUntil) == until.UTC().Format(time.RFC3339Nano)
			}
		}
		if !found {
			t.Fatal("ledger list did not expose snooze")
		}
		if err := cli.UnsnoozeSession("self", "self"); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitSession(app, "self", func(s protocol.Session) bool { return s.TurnSnoozedUntil == nil && protocol.Deref(s.TurnOwed) })
		for _, tc := range []struct {
			target string
			until  time.Time
			want   string
		}{
			{"missing", until, "unknown live session missing"},
			{"self", time.Now().Add(-time.Minute), "in the past"},
			{"other", until, "may snooze itself"},
		} {
			if err := cli.SnoozeSession("self", protocol.SessionID(tc.target), tc.until); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("snooze %s: %v; want %s", tc.target, err, tc.want)
			}
		}
		if err := cli.UnsnoozeSession("self", "missing"); err == nil || !strings.Contains(err.Error(), "unknown live session missing") {
			t.Errorf("unknown unsnooze: %v", err)
		}
		if err := cli.UnsnoozeSession("self", "self"); err != nil {
			t.Fatal(err)
		}
		if err := cli.SnoozeSession("self", "self", until); err != nil {
			t.Fatal(err)
		}
		w.advance(time.Hour)
		shown, err = cli.SessionShow("self")
		if err != nil || shown.Entry.TurnSnoozedUntil != nil {
			t.Fatalf("expired ledger snooze: %+v %v", shown, err)
		}
	})
}

func TestSnoozePermissionsIncludeDispatchersAndProfileChief(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	source := w.Spawn(app, fakeagent.Claude, w.Path("source"))
	other := w.Spawn(app, fakeagent.Claude, w.Path("other"))
	w.Launched(source)
	w.Launched(other)
	delegated, err := cli.Delegate(delegateFrom(source, w.Path("source"), "Continue the work", fakeagent.Claude))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(delegated.SessionID)).Prompted()
	until := time.Now().Add(time.Hour)
	if err := cli.SnoozeSession(protocol.SessionID(source), delegated.SessionID, until); err != nil {
		t.Fatal(err)
	}
	if err := cli.UnsnoozeSession(protocol.SessionID(source), delegated.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := cli.SnoozeSession(protocol.SessionID(other), delegated.SessionID, until); err == nil || !strings.Contains(err.Error(), "may snooze itself") {
		t.Fatalf("other dispatcher: %v", err)
	}
	chief := configureChiefOn(t, w, app, fakeagent.Claude, "sonnet")
	w.Launched(chief).Prompted()
	if err := cli.SnoozeSession(protocol.SessionID(chief), protocol.SessionID(other), until); err != nil {
		t.Fatal(err)
	}
	if err := cli.UnsnoozeSession(protocol.SessionID(chief), protocol.SessionID(other)); err != nil {
		t.Fatal(err)
	}
	side := createProfile(app, "Side")
	selectProfile(app, side.ID)
	foreign := w.Spawn(w.AppOn(side.ID), fakeagent.Claude, w.Path("foreign"), func(m *protocol.SpawnSessionMessage) { m.Label = protocol.Ptr("private foreign work") })
	w.Launched(foreign)
	for _, action := range []func() error{
		func() error { return cli.SnoozeSession(protocol.SessionID(chief), protocol.SessionID(foreign), until) },
		func() error { return cli.UnsnoozeSession(protocol.SessionID(chief), protocol.SessionID(foreign)) },
	} {
		if err := action(); err == nil || !strings.Contains(err.Error(), "unknown live session") || strings.Contains(err.Error(), "private foreign work") {
			t.Fatalf("cross-profile operation: %v", err)
		}
	}
}

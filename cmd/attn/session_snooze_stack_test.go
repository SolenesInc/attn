package main_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSessionSnoozeCLIShowsBatchDeadlinesAndUnsnoozes(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	first := s.Spawn(app, fakeagent.Claude, s.Path("first"))
	second := s.Spawn(app, fakeagent.Claude, s.Path("second"))
	for _, id := range []string{first, second} {
		run := s.Launched(id)
		app.TypeLine(id, "Finish this turn")
		run.Prompted()
		run.Reply("Done. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, id, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	}
	until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	snoozed := s.Attn("session", "snooze", first, second, "--until", until)
	if snoozed.Code != 0 || !strings.Contains(snoozed.Stdout, first+" snoozed until "+until) || !strings.Contains(snoozed.Stdout, second+" snoozed until "+until) {
		t.Fatalf("batch snooze: %+v", snoozed)
	}
	t.Logf("batch snooze output:\n%s", snoozed.Stdout)
	for _, id := range []string{first, second} {
		testworld.AwaitSession(app, id, func(x protocol.Session) bool {
			return protocol.Deref(x.TurnSnoozedUntil) == until && !protocol.Deref(x.TurnOwed)
		})
		shown := s.Attn("session", "show", id)
		if shown.Code != 0 || !strings.Contains(shown.Stdout, "snoozed until "+until) {
			t.Fatalf("show snooze: %+v", shown)
		}
	}
	listed := s.Attn("session", "list")
	if listed.Code != 0 || strings.Count(listed.Stdout, until) != 2 {
		t.Fatalf("list snoozes: %+v", listed)
	}
	t.Logf("session list output:\n%s", listed.Stdout)
	var page protocol.SessionListResult
	s.Attn("session", "list", "--json").JSON(t, &page)
	for _, entry := range page.Entries {
		if protocol.Deref(entry.TurnSnoozedUntil) != until {
			t.Errorf("JSON ledger missing deadline: %+v", entry)
		}
	}
	woken := s.Run(testworld.Invocation{Session: first, Args: []string{"session", "unsnooze", first}})
	if woken.Code != 0 {
		t.Fatalf("unsnooze: %+v", woken)
	}
	t.Logf("unsnooze output:\n%s", woken.Stdout)
	testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) && x.TurnSnoozedUntil == nil })
	if again := s.Attn("session", "unsnooze", first); again.Code != 0 {
		t.Fatalf("idempotent unsnooze: %+v", again)
	}
	var remaining protocol.SessionListResult
	s.Attn("session", "list", "--json").JSON(t, &remaining)
	for _, entry := range remaining.Entries {
		want := ""
		if string(entry.ID) == second {
			want = until
		}
		if protocol.Deref(entry.TurnSnoozedUntil) != want {
			t.Errorf("after unsnooze: %+v; want deadline %q", entry, want)
		}
	}

	partial := s.Run(testworld.Invocation{Session: first, Args: []string{"session", "snooze", second, first, "missing", "--until", "1h"}})
	if partial.Code != 1 || !strings.Contains(partial.Stdout, first+" snoozed until") || !strings.Contains(partial.Stderr, "may snooze itself") || !strings.Contains(partial.Stderr, "unknown live session missing") {
		t.Fatalf("partial batch: %+v", partial)
	}
	denied := s.Run(testworld.Invocation{Session: first, Args: []string{"session", "unsnooze", second}})
	if denied.Code != 1 || !strings.Contains(denied.Stderr, "may unsnooze itself") {
		t.Fatalf("unsnooze permissions: %+v", denied)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"session", "snooze", first}, "--until is required"},
		{[]string{"session", "snooze", first, "--until", "2020-01-01T00:00:00Z"}, "in the past"},
		{[]string{"session", "snooze", "--until", "1h"}, "at least one session id"},
	} {
		got := s.Attn(tc.args...)
		if got.Code != 2 || !strings.Contains(got.Stderr, tc.want) {
			t.Errorf("%v: %+v", tc.args, got)
		}
	}
	for _, args := range [][]string{{"session", "snooze", "--help"}, {"skill", "--reference", "converse-and-observe"}} {
		got := s.Attn(args...)
		if got.Code != 0 || !strings.Contains(got.Stdout, "unsnooze") {
			t.Errorf("discovery %v: %+v", args, got)
		}
	}
}

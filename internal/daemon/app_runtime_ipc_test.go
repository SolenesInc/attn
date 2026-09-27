package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

func TestInvocationRetentionTrimsByAgeAcrossEveryApp(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "greeter", subscribing("ticket.*"))

	old := clock.Now().Add(-AppInvocationRetention - time.Hour)
	fresh := clock.Now().Add(-time.Hour)
	for _, row := range []struct {
		app  string
		when time.Time
	}{
		{"greeter", old},
		{"greeter", fresh},
		{"removed-app", old},
		{"removed-app", fresh},
	} {
		if _, err := d.store.AppendAppInvocation(store.AppInvocation{
			AppName: row.app, VersionID: 1, EventSeq: 1, EventName: "ticket.created",
			Handler: "ticket.*", Status: appInvocationStatusOK, StartedAt: row.when,
		}); err != nil {
			t.Fatalf("append invocation: %v", err)
		}
	}

	result, err := d.appInvocationRetentionHandler(t.Context(), &jobs.Job{})
	if err != nil {
		t.Fatalf("retention pass: %v", err)
	}
	if removed := result.(map[string]any)["removed"]; removed != 2 {
		t.Fatalf("removed = %v, want 2 — one per app, including the removed one", removed)
	}
	for _, app := range []string{"greeter", "removed-app"} {
		rows, err := d.store.ListAppInvocations(app, 10)
		if err != nil {
			t.Fatalf("list invocations for %s: %v", app, err)
		}
		if len(rows) != 1 || !rows[0].StartedAt.Equal(fresh.UTC()) {
			t.Fatalf("%s kept %+v, want only the fresh row", app, rows)
		}
	}
}

func TestInvocationRetentionCapsEachAppAtItsNewestRows(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "loud", subscribing("ticket.*"))

	const cap = 5
	for i := 0; i < cap+4; i++ {
		if _, err := d.store.AppendAppInvocation(store.AppInvocation{
			AppName: "loud", VersionID: 1, EventSeq: int64(i), EventName: "ticket.created",
			Handler: "ticket.*", Status: appInvocationStatusOK,
			StartedAt: clock.Now().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append invocation: %v", err)
		}
	}

	removed, err := d.store.TrimAppInvocations(clock.Now().Add(-AppInvocationRetention), cap)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if removed != 4 {
		t.Fatalf("removed = %d, want 4 — nine rows capped at five", removed)
	}
	rows, err := d.store.ListAppInvocations("loud", 100)
	if err != nil {
		t.Fatalf("list invocations: %v", err)
	}
	if len(rows) != cap {
		t.Fatalf("kept %d rows, want %d", len(rows), cap)
	}
	for i, row := range rows {
		if want := int64(cap + 3 - i); row.EventSeq != want {
			t.Fatalf("row %d is seq %d, want %d — the cap dropped the wrong end", i, row.EventSeq, want)
		}
	}
}

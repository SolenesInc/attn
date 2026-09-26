package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestAppLogTagMatchesTheHost(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "apphost", "src", "index.ts"))
	if err != nil {
		t.Fatalf("read the app runtime host: %v", err)
	}
	if !strings.Contains(string(source), "[app ${app}] ") {
		t.Fatalf("the host does not write the per-app tag %q that `attn app logs` filters on", appRuntimeAppTag("<name>"))
	}
	if !strings.Contains(string(source), appRuntimeSelfTag) {
		t.Fatalf("the host does not write the runtime tag %q", appRuntimeSelfTag)
	}
}

func TestAppWatchStreamsInvocationsAsTheyHappen(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	installApp(t, d, "auditor", subscribing("ticket.*"))
	startFakeAppRuntime(t, d, nil)

	watcher := &appWatcher{app: "greeter", events: make(chan protocol.AppInvocationInfo, 4)}
	d.addAppWatcher(watcher)
	t.Cleanup(func() { d.removeAppWatcher(watcher) })

	if err := d.deliverAppEvent(t.Context(), "auditor", appEvent("ticket.created", "tk-1", 1)); err != nil {
		t.Fatalf("deliver to auditor: %v", err)
	}
	if err := d.deliverAppEvent(t.Context(), "greeter", appEvent("ticket.created", "tk-2", 2)); err != nil {
		t.Fatalf("deliver to greeter: %v", err)
	}

	select {
	case info := <-watcher.events:
		if protocol.Deref(info.EventSubject) != "tk-2" {
			t.Fatalf("the stream carried %+v, want greeter's own invocation", info)
		}
		if info.Status != appInvocationStatusOK || info.Handler != apps.SubscriptionLabel("ticket.*") {
			t.Fatalf("streamed invocation = %+v", info)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the invocation never reached the watcher")
	}
	if len(watcher.events) != 0 {
		t.Fatalf("%d extra invocation(s) reached a watcher of greeter", len(watcher.events))
	}
}

func TestASlowWatcherIsDroppedRatherThanBlockingDelivery(t *testing.T) {
	d := newAppDaemon(t)
	watcher := &appWatcher{app: "greeter", events: make(chan protocol.AppInvocationInfo)}
	d.addAppWatcher(watcher)
	t.Cleanup(func() { d.removeAppWatcher(watcher) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.notifyAppWatchers(protocol.AppInvocationInfo{EventSubject: protocol.Ptr("tk-1")}, "greeter")
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a watcher nobody is reading blocked the delivery path")
	}
}

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

func TestAppStatusCarriesTheStallClockAndWhenItFires(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	startFakeAppRuntime(t, d, func(*fakeAppRuntime, appDispatchRequest) error {
		return errors.New("ReferenceError: ticket is not defined")
	})

	if err := d.deliverAppEvent(t.Context(), "greeter", appEvent("ticket.created", "tk-1", 9)); err == nil {
		t.Fatal("a throwing handler reported success")
	}
	resp := appStatus(t, d, "greeter")
	if !resp.Ok {
		t.Fatalf("app status: %v", protocol.Deref(resp.Error))
	}
	stall := resp.AppStatusResult.Stall
	if stall == nil {
		t.Fatal("a stalled app's status carried no stall")
	}
	if stall.Kind != appStallKindSubscription || protocol.Deref(stall.EventSeq) != 9 ||
		protocol.Deref(stall.EventName) != "ticket.created" || stall.Attempts != 1 {
		t.Fatalf("stall = %+v", stall)
	}
	if !strings.Contains(stall.LastError, "ReferenceError") {
		t.Fatalf("stall does not say what failed: %q", stall.LastError)
	}
	want := stampForWire(clock.Now().Add(appAutoDisableStall))
	if stall.DisablesAt != want {
		t.Fatalf("disables at %q, want %q", stall.DisablesAt, want)
	}

	d.clearAppStall("greeter")
	if again := appStatus(t, d, "greeter"); again.AppStatusResult.Stall != nil {
		t.Fatalf("a recovered app still reports a stall: %+v", again.AppStatusResult.Stall)
	}
}

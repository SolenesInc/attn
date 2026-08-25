package daemon

import (
	"errors"
	"net"
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

func appLogs(t *testing.T, d *Daemon, name string, lines int) protocol.Response {
	t.Helper()
	msg := &protocol.AppLogsMessage{Cmd: protocol.CmdAppLogs, Name: name}
	if lines > 0 {
		msg.Lines = protocol.Ptr(lines)
	}
	return docCall(t, func(c net.Conn) { d.handleAppLogs(c, msg) })
}

func writeRuntimeLog(t *testing.T, d *Daemon, lines ...string) {
	t.Helper()
	path := AppRuntimeLogPath(d.socketPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write runtime log: %v", err)
	}
}

func TestAppLogsFiltersByTagAndRuntimeShowsEverything(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	writeRuntimeLog(t, d,
		appRuntimeSelfTag+"starting, api version 1",
		appRuntimeAppTag("greeter")+"hello from greeter",
		appRuntimeAppTag("auditor")+"hello from auditor",
		appRuntimeAppTag("greeter")+"and again",
	)

	resp := appLogs(t, d, "greeter", 0)
	if !resp.Ok {
		t.Fatalf("app logs greeter: %v", protocol.Deref(resp.Error))
	}
	got := resp.AppLogsResult.Lines
	want := []string{"hello from greeter", "and again"}
	if len(got) != len(want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if resp.AppLogsResult.Path != AppRuntimeLogPath(d.socketPath) {
		t.Fatalf("path = %q, want the runtime log", resp.AppLogsResult.Path)
	}

	whole := appLogs(t, d, appRuntimeChildName, 0)
	if !whole.Ok {
		t.Fatalf("app logs runtime: %v", protocol.Deref(whole.Error))
	}
	if len(whole.AppLogsResult.Lines) != 4 {
		t.Fatalf("runtime log lines = %q, want all four", whole.AppLogsResult.Lines)
	}
	if !strings.Contains(whole.AppLogsResult.Lines[0], "api version 1") {
		t.Fatalf("the runtime's own lines are missing: %q", whole.AppLogsResult.Lines)
	}
}

// The tag is written in TypeScript and read in Go; nothing but this test keeps the
// spellings the same, and a drift makes `attn app logs <name>` return nothing.
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

func TestAppLogsBeforeTheRuntimeEverRanIsEmptyNotAnError(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))

	resp := appLogs(t, d, "greeter", 0)
	if !resp.Ok {
		t.Fatalf("app logs before any runtime ran: %v", protocol.Deref(resp.Error))
	}
	if len(resp.AppLogsResult.Lines) != 0 {
		t.Fatalf("lines = %q, want none", resp.AppLogsResult.Lines)
	}
	if resp.AppLogsResult.Path == "" {
		t.Fatal("an empty answer did not say where the log would be")
	}
}

func TestAppLogsRefusesAnAskPastItsCeilingByName(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))

	resp := appLogs(t, d, "greeter", appLogMaxLines+1)
	if resp.Ok {
		t.Fatal("an ask past the ceiling was served")
	}
	msg := protocol.Deref(resp.Error)
	for _, want := range []string{"10001", "10000", AppRuntimeLogPath(d.socketPath)} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal does not name %q: %s", want, msg)
		}
	}
}

func TestAppLogsSaysWhenItDroppedOlderLines(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	lines := make([]string, 0, 5)
	for _, text := range []string{"one", "two", "three", "four", "five"} {
		lines = append(lines, appRuntimeAppTag("greeter")+text)
	}
	writeRuntimeLog(t, d, lines...)

	resp := appLogs(t, d, "greeter", 2)
	if !resp.Ok {
		t.Fatalf("app logs: %v", protocol.Deref(resp.Error))
	}
	if !resp.AppLogsResult.Truncated {
		t.Fatal("older lines were dropped without saying so")
	}
	if got := resp.AppLogsResult.Lines; len(got) != 2 || got[0] != "four" || got[1] != "five" {
		t.Fatalf("lines = %q, want the last two", got)
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

// The age window cannot bound the table on its own: how many rows thirty days holds
// depends on what the app subscribed to.
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

// `runtime` in particular: `attn app logs runtime` already means the shared
// process.
func TestApplyRefusesAReservedAppName(t *testing.T) {
	d := newAppDaemon(t)
	for _, name := range apps.ReservedNames() {
		resp := docCall(t, func(c net.Conn) {
			d.handleAppApply(c, &protocol.AppApplyMessage{
				Cmd: protocol.CmdAppApply, Name: name,
				ContentHash: "sha256:whatever",
				Declaration: `{"name":"` + name + `","subscribe":[{"events":["ticket.*"]}]}`,
			})
		})
		if resp.Ok {
			t.Fatalf("apply installed an app called %q", name)
		}
		if !strings.Contains(protocol.Deref(resp.Error), "reserved") {
			t.Fatalf("apply of %q did not say the name is reserved: %s", name, protocol.Deref(resp.Error))
		}
	}
	if rows, err := d.store.ListApps(); err != nil || len(rows) != 0 {
		t.Fatalf("apps = %+v (err %v), want none installed", rows, err)
	}
}

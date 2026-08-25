package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type appTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newAppTestClock(d *Daemon) *appTestClock {
	clock := &appTestClock{now: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)}
	d.appClock = clock.Now
	return clock
}

func (c *appTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *appTestClock) advance(by time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(by)
	c.mu.Unlock()
}

func appEnabled(t *testing.T, d *Daemon, name string) bool {
	t.Helper()
	consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName(name))
	if err != nil || !ok {
		t.Fatalf("consumer for %s: %v ok=%t", name, err, ok)
	}
	return consumer.Enabled
}

func appNotifications(t *testing.T, d *Daemon, kind string) []store.NotificationRecord {
	t.Helper()
	all, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	var out []store.NotificationRecord
	for _, record := range all {
		if record.Kind == kind {
			out = append(out, record)
		}
	}
	return out
}

func failEvery(message string) func(*fakeAppRuntime, appDispatchRequest) error {
	return func(*fakeAppRuntime, appDispatchRequest) error { return errors.New(message) }
}

func TestAppStuckOnOneEventIsDisabledAndSaysSoThreeWays(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	startFakeAppRuntime(t, d, failEvery("TypeError: undefined is not a function"))
	stuck := appEvent("ticket.created", "tk-1", 12)

	for i := 0; i < 4; i++ {
		if err := d.deliverAppEvent(context.Background(), "greeter", stuck); err == nil {
			t.Fatal("a throwing handler reported success")
		}
		clock.advance(3 * time.Minute)
		if !appEnabled(t, d, "greeter") {
			t.Fatalf("greeter was disabled after %d failure(s) inside the window", i+1)
		}
	}

	clock.advance(4 * time.Minute)
	if err := d.deliverAppEvent(context.Background(), "greeter", stuck); err == nil {
		t.Fatal("a throwing handler reported success")
	}

	if appEnabled(t, d, "greeter") {
		t.Fatal("greeter is still enabled after 15 minutes stuck on one event")
	}
	facts := appFacts(t, d, FactAppEnabledChanged)
	if len(facts) != 1 || facts[0].Subject != "greeter" {
		t.Fatalf("app.enabled.changed facts = %+v, want one for greeter", facts)
	}
	notes := appNotifications(t, d, notificationKindAppAutoDisabled)
	if len(notes) != 1 {
		t.Fatalf("auto-disable notifications = %d, want 1", len(notes))
	}
	if !strings.Contains(notes[0].Body, "attn app enable greeter") {
		t.Fatalf("the notification does not name the way back: %q", notes[0].Body)
	}
	if !strings.Contains(notes[0].Body, "ticket.created") || !strings.Contains(notes[0].Detail, "TypeError") {
		t.Fatalf("the notification does not say what failed: body=%q detail=%q", notes[0].Body, notes[0].Detail)
	}
	if notes[0].Severity != store.NotificationWarning {
		t.Fatalf("severity = %q, want warning", notes[0].Severity)
	}
	if created := appFacts(t, d, FactNotificationCreated); len(created) != 1 || created[0].Subject != notes[0].ID {
		t.Fatalf("notification.created facts = %+v, want one for %s", created, notes[0].ID)
	}
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("the disabled app is still on the clock: %+v", stall)
	}
}

func TestSlowButSucceedingAppIsNeverDisabled(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "slowpoke", subscribing("ticket.*"))
	startFakeAppRuntime(t, d, func(*fakeAppRuntime, appDispatchRequest) error {
		clock.advance(10 * time.Minute)
		return nil
	})

	for seq := int64(1); seq <= 6; seq++ {
		if err := d.deliverAppEvent(context.Background(), "slowpoke", appEvent("ticket.created", "tk", seq)); err != nil {
			t.Fatalf("a succeeding handler failed: %v", err)
		}
	}
	if !appEnabled(t, d, "slowpoke") {
		t.Fatal("an app that succeeded on every event, slowly, was disabled")
	}
	if _, ok := d.appStallSnapshot("slowpoke"); ok {
		t.Fatal("a succeeding app is on the auto-disable clock")
	}
	rows := invocationsOf(t, d, "slowpoke")
	if len(rows) != 6 {
		t.Fatalf("recorded %d invocation(s), want 6", len(rows))
	}
	if rows[0].Duration < 10*time.Minute {
		t.Fatalf("recorded duration %s, want the ten minutes the handler took", rows[0].Duration)
	}
}

func TestAppFailingOnDistinctEventsIsNeverDisabled(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "flaky", subscribing("ticket.*"))
	startFakeAppRuntime(t, d, failEvery("intermittent"))

	for seq := int64(1); seq <= 10; seq++ {
		if err := d.deliverAppEvent(context.Background(), "flaky", appEvent("ticket.created", "tk", seq)); err == nil {
			t.Fatal("a throwing handler reported success")
		}
		clock.advance(5 * time.Minute)
		if !appEnabled(t, d, "flaky") {
			t.Fatalf("flaky was disabled after failing on %d distinct events", seq)
		}
	}
	stall, ok := d.appStallSnapshot("flaky")
	if !ok || stall.seq != 10 || stall.attempts != 1 {
		t.Fatalf("stall = %+v (present=%t), want a fresh clock on the last event", stall, ok)
	}
}

func TestARuntimeOutageLongerThanTheWindowDisablesNothing(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	t.Setenv(appRuntimeHostOverride, writeExecutableStub(t, "exit 0"))
	d.appRuntimeSupervise.GiveUpAfter = 1
	// The connect wait is the one thing here that is real time rather than the
	// injected clock, and the runtime is never going to connect.
	d.appRuntimeWait = 20 * time.Millisecond

	stuck := appEvent("ticket.created", "tk-1", 4)
	if err := d.deliverAppEvent(context.Background(), "greeter", stuck); !isRuntimeFailure(err) {
		t.Fatalf("error %v was not classified as the runtime's", err)
	}
	clock.advance(2 * appAutoDisableStall)
	if err := d.deliverAppEvent(context.Background(), "greeter", stuck); !isRuntimeFailure(err) {
		t.Fatalf("error %v was not classified as the runtime's", err)
	}

	if !appEnabled(t, d, "greeter") {
		t.Fatal("an app was disabled because the runtime was down")
	}
	if notes := appNotifications(t, d, notificationKindAppAutoDisabled); len(notes) != 0 {
		t.Fatalf("a runtime outage wrote %d auto-disable notification(s)", len(notes))
	}
}

func TestEnablingClearsTheStreakAndResumesDelivery(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	var throw = true
	startFakeAppRuntime(t, d, func(*fakeAppRuntime, appDispatchRequest) error {
		if throw {
			return errors.New("boom")
		}
		return nil
	})
	stuck := appEvent("ticket.created", "tk-1", 3)

	_ = d.deliverAppEvent(context.Background(), "greeter", stuck)
	clock.advance(appAutoDisableStall + time.Minute)
	_ = d.deliverAppEvent(context.Background(), "greeter", stuck)
	if appEnabled(t, d, "greeter") {
		t.Fatal("the app was not disabled")
	}

	resp := appSetEnabled(t, d, "greeter", true)
	if !resp.Ok {
		t.Fatalf("enable: %v", protocol.Deref(resp.Error))
	}
	if !appEnabled(t, d, "greeter") {
		t.Fatal("enable did not flip the consumer bit back")
	}
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("enable left the old stall clock running: %+v", stall)
	}

	_ = d.deliverAppEvent(context.Background(), "greeter", stuck)
	if !appEnabled(t, d, "greeter") {
		t.Fatal("a re-enabled app was disabled again by its first failure")
	}
	stall, ok := d.appStallSnapshot("greeter")
	if !ok || !stall.since.Equal(clock.Now()) {
		t.Fatalf("stall = %+v (present=%t), want a window starting now", stall, ok)
	}

	throw = false
	if err := d.deliverAppEvent(context.Background(), "greeter", stuck); err != nil {
		t.Fatalf("a re-enabled app did not deliver: %v", err)
	}
}

func TestADisabledInstalledAppStillHoldsTheRetentionFloor(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "greeter", subscribing(FactDocumentChanged))
	startFakeAppRuntime(t, d, failEvery("boom"))

	for i := 0; i < 4; i++ {
		d.publishFact(FactDocumentChanged, "app/greeter/seen/tk-1", nil)
	}
	waitFor(t, "the app to stall on the first fact", func() bool {
		_, ok := d.appStallSnapshot("greeter")
		return ok
	})

	removed, err := d.eventBus.Trim()
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if removed != 0 {
		t.Fatalf("compaction removed %d event(s) while a stalled enabled consumer sat at the bottom of the log", removed)
	}

	clock.advance(appAutoDisableStall + time.Minute)
	waitFor(t, "the stalled app to be disabled", func() bool { return !appEnabled(t, d, "greeter") })

	removed, err = d.eventBus.Trim()
	if err != nil {
		t.Fatalf("trim after the auto-disable: %v", err)
	}
	if removed != 0 {
		t.Fatalf("compaction removed %d retained event(s) after auto-disable", removed)
	}
}

// Both notifications rounded the stall to the minute, so a moved window read
// "for 0s": the constant repeated back instead of a measurement.
func TestAnAutoDisableNotificationReportsHowLongAttnTried(t *testing.T) {
	const window = 25 * time.Second

	t.Run("a handler stuck on one event", func(t *testing.T) {
		d := newAppDaemon(t)
		clock := newAppTestClock(d)
		d.appAutoDisableWait = window
		installApp(t, d, "greeter", subscribing("ticket.*"))
		startFakeAppRuntime(t, d, failEvery("TypeError: undefined is not a function"))
		stuck := appEvent("ticket.created", "tk-1", 12)

		if err := d.deliverAppEvent(context.Background(), "greeter", stuck); err == nil {
			t.Fatal("a throwing handler reported success")
		}
		clock.advance(30 * time.Second)
		if err := d.deliverAppEvent(context.Background(), "greeter", stuck); err == nil {
			t.Fatal("a throwing handler reported success")
		}
		assertDisableNotificationSaysDuration(t, d, "30s")
	})

	t.Run("a rebuild that keeps throwing", func(t *testing.T) {
		d := newAppDaemon(t)
		clock := newAppTestClock(d)
		d.appAutoDisableWait = window
		reconcilingApp(t, d, "greeter")
		runtime := startFakeAppRuntime(t, d, nil)
		runtime.reconcile = func(*fakeAppRuntime, appReconcileRequest) error {
			return errors.New("TypeError: snapshot.sessions is not iterable")
		}

		if err := appReconcilePreDrain(t, d, "greeter"); err == nil {
			t.Fatal("a throwing reconcile reported success")
		}
		clock.advance(30 * time.Second)
		if err := appReconcilePreDrain(t, d, "greeter"); err == nil {
			t.Fatal("a throwing reconcile reported success")
		}
		assertDisableNotificationSaysDuration(t, d, "30s")
	})
}

func assertDisableNotificationSaysDuration(t *testing.T, d *Daemon, want string) {
	t.Helper()
	if appEnabled(t, d, "greeter") {
		t.Fatal("greeter is still enabled past its stall window")
	}
	notes := appNotifications(t, d, notificationKindAppAutoDisabled)
	if len(notes) != 1 {
		t.Fatalf("auto-disable notifications = %d, want 1", len(notes))
	}
	if !strings.Contains(notes[0].Body, "for "+want) {
		t.Fatalf("the notification does not report how long attn tried (want %q): %q", want, notes[0].Body)
	}
}

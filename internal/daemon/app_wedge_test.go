package daemon

import (
	"context"
	"strings"
	"testing"
	"time"
)

func enterOnceThenBlock(entered chan<- string, release <-chan struct{}) func(*fakeAppRuntime, appDispatchRequest) error {
	return func(f *fakeAppRuntime, req appDispatchRequest) error {
		entered <- req.App
		<-release
		return nil
	}
}

func TestFrozenLoopChargesTheHandlerOnTheLoopNotTheEarliestDispatch(t *testing.T) {
	d := newAppDaemon(t)
	// The shipped tripwires are 60s and 2s; waiting them out proves nothing extra.
	d.appDispatchWait = 300 * time.Millisecond
	d.appPingWait = 50 * time.Millisecond
	installApp(t, d, "bystander", subscribing("ticket.*"))
	installApp(t, d, "hog", subscribing("ticket.*"))
	installApp(t, d, "latecomer", subscribing("ticket.*"))

	entered := make(chan string, 2)
	release := make(chan struct{})
	defer close(release)
	runtime := startFakeAppRuntime(t, d, enterOnceThenBlock(entered, release))

	failures := map[string]chan error{"bystander": make(chan error, 1), "hog": make(chan error, 1), "latecomer": make(chan error, 1)}
	for i, name := range []string{"bystander", "hog"} {
		go func() {
			failures[name] <- d.deliverAppEvent(context.Background(), name, appEvent("ticket.created", "tk", int64(i+1)))
		}()
		if got := <-entered; got != name {
			t.Fatalf("handler %d in was %s, want %s", i, got, name)
		}
	}
	runtime.freezeLoop()

	go func() {
		failures["latecomer"] <- d.deliverAppEvent(context.Background(), "latecomer", appEvent("ticket.created", "tk-3", 3))
	}()

	for name, ch := range failures {
		if err := <-ch; err == nil {
			t.Fatalf("%s's dispatch into a frozen runtime reported success", name)
		} else if name != "hog" && !strings.Contains(err.Error(), "hog") {
			t.Fatalf("%s's failure does not name the culprit: %v", name, err)
		}
	}

	if _, ok := d.appStallSnapshot("hog"); !ok {
		t.Fatal("hog wedged the runtime and is not on the auto-disable clock")
	}
	if rows := invocationsOf(t, d, "hog"); len(rows) != 1 || rows[0].Status != appInvocationStatusError {
		t.Fatalf("hog's invocation = %+v, want one with status %q", rows, appInvocationStatusError)
	}

	for _, name := range []string{"bystander", "latecomer"} {
		if stall, ok := d.appStallSnapshot(name); ok {
			t.Fatalf("%s was charged for hog's wedge: %+v", name, stall)
		}
		if rows := invocationsOf(t, d, name); len(rows) != 1 || rows[0].Status != appInvocationStatusRuntimeError {
			t.Fatalf("%s's invocation = %+v, want one with status %q", name, rows, appInvocationStatusRuntimeError)
		}
	}
}

func TestAVictimIsNotChargedAfterTheCulpritsDispatchHasGivenUp(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 200 * time.Millisecond
	d.appPingWait = 50 * time.Millisecond
	installApp(t, d, "hog", subscribing("ticket.*"))
	installApp(t, d, "latecomer", subscribing("ticket.*"))

	entered := make(chan string, 1)
	release := make(chan struct{})
	defer close(release)
	runtime := startFakeAppRuntime(t, d, enterOnceThenBlock(entered, release))

	go d.deliverAppEvent(context.Background(), "hog", appEvent("ticket.created", "tk-1", 1))
	if app := <-entered; app != "hog" {
		t.Fatalf("first handler in was %s, want hog", app)
	}
	runtime.freezeLoop()

	waitFor(t, "hog's dispatch to be abandoned", func() bool {
		_, ok := d.appStallSnapshot("hog")
		return ok
	})

	err := d.deliverAppEvent(context.Background(), "latecomer", appEvent("ticket.created", "tk-2", 2))
	if err == nil {
		t.Fatal("a dispatch into a frozen runtime reported success")
	}
	if stall, ok := d.appStallSnapshot("latecomer"); ok {
		t.Fatalf("latecomer was charged for a loop hog had already frozen: %+v", stall)
	}
	if !strings.Contains(err.Error(), "hog") {
		t.Fatalf("failure does not name the culprit: %v", err)
	}
}

func TestAHandlerThatYieldedIsStillNamedWhenItComesBackAndSpins(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 200 * time.Millisecond
	d.appPingWait = 100 * time.Millisecond
	installApp(t, d, "sleeper", subscribing("ticket.*"))
	installApp(t, d, "latecomer", subscribing("ticket.*"))

	entered := make(chan string, 1)
	release := make(chan struct{})
	defer close(release)
	runtime := startFakeAppRuntime(t, d, enterOnceThenBlock(entered, release))

	err := d.deliverAppEvent(context.Background(), "sleeper", appEvent("ticket.created", "tk-1", 1))
	if err == nil {
		t.Fatal("a handler that never returned reported success")
	}
	if !strings.Contains(err.Error(), "did not return within") {
		t.Fatalf("failure = %v, want the hung-handler message on a turning loop", err)
	}

	runtime.freezeLoop()

	failure := d.deliverAppEvent(context.Background(), "latecomer", appEvent("ticket.created", "tk-2", 2))
	if failure == nil {
		t.Fatal("a dispatch into a frozen runtime reported success")
	}
	if !strings.Contains(failure.Error(), "sleeper") {
		t.Fatalf("the handler that yielded and came back is no longer named: %v", failure)
	}
	if stall, ok := d.appStallSnapshot("latecomer"); ok {
		t.Fatalf("latecomer was charged for a loop sleeper froze: %+v", stall)
	}
}

func TestAHandlerThatAlreadyReturnedIsNotBlamedForALaterFreeze(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 200 * time.Millisecond
	d.appPingWait = 50 * time.Millisecond
	installApp(t, d, "quick", subscribing("ticket.*"))
	installApp(t, d, "unlucky", subscribing("ticket.*"))

	runtime := startFakeAppRuntime(t, d, nil)
	if err := d.deliverAppEvent(context.Background(), "quick", appEvent("ticket.created", "tk-1", 1)); err != nil {
		t.Fatalf("a handler that returns normally failed: %v", err)
	}

	runtime.freezeLoop()

	err := d.deliverAppEvent(context.Background(), "unlucky", appEvent("ticket.created", "tk-2", 2))
	if err == nil {
		t.Fatal("a dispatch into a frozen runtime reported success")
	}
	if strings.Contains(err.Error(), "quick") {
		t.Fatalf("an app that had already returned was blamed: %v", err)
	}
	if _, ok := d.appStallSnapshot("quick"); ok {
		t.Fatal("an app that had already returned was put on the auto-disable clock")
	}
	if _, ok := d.appStallSnapshot("unlucky"); !ok {
		t.Fatal("with no culprit to name, the app whose dispatch timed out must still be charged")
	}
}

func TestATurningLoopChargesTheAppWhoseHandlerHung(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 300 * time.Millisecond
	d.appPingWait = 2 * time.Second
	installApp(t, d, "dawdler", subscribing("ticket.*"))

	entered := make(chan string, 1)
	release := make(chan struct{})
	defer close(release)
	startFakeAppRuntime(t, d, enterOnceThenBlock(entered, release))

	err := d.deliverAppEvent(context.Background(), "dawdler", appEvent("ticket.created", "tk-1", 1))
	if err == nil {
		t.Fatal("a handler that never returned reported success")
	}
	if !strings.Contains(err.Error(), "did not return within") {
		t.Fatalf("failure = %v, want the hung-handler message", err)
	}
	if _, ok := d.appStallSnapshot("dawdler"); !ok {
		t.Fatal("a hung handler must put its own app on the auto-disable clock")
	}
	if rows := invocationsOf(t, d, "dawdler"); len(rows) != 1 || rows[0].Status != appInvocationStatusError {
		t.Fatalf("invocation = %+v, want one with status %q", rows, appInvocationStatusError)
	}
}

func TestTheLivenessPingIsBoundedIndependently(t *testing.T) {
	if appRuntimePingWait <= 0 {
		t.Fatal("the liveness ping must be bounded")
	}
	if appRuntimePingWait >= appDispatchTimeout {
		t.Fatalf("ping wait %v is not comfortably inside the dispatch timeout %v", appRuntimePingWait, appDispatchTimeout)
	}
	d := &Daemon{}
	if got := d.appPingBudget(); got != appRuntimePingWait {
		t.Fatalf("appPingBudget() = %v, want the shipped %v", got, appRuntimePingWait)
	}
	d.appPingWait = 5 * time.Millisecond
	if got := d.appPingBudget(); got != 5*time.Millisecond {
		t.Fatalf("appPingBudget() = %v, want the override", got)
	}
}

func TestAWedgedDispatchEndsTheRuntimeGenerationAndTheSupervisorReplacesIt(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 300 * time.Millisecond
	d.appPingWait = 50 * time.Millisecond
	installApp(t, d, "hog", subscribing("ticket.*"))

	t.Setenv(appRuntimeHostOverride, writeExecutableStub(t, "exec sleep 300"))
	t.Cleanup(d.stopAppRuntime)
	if err := d.ensureAppRuntime(); err != nil {
		t.Fatalf("start the supervised runtime: %v", err)
	}
	wedged, ok := d.appRuntimeSnapshot()
	if !ok || !wedged.Running {
		t.Fatalf("supervised runtime snapshot = %+v, want a running child", wedged)
	}

	entered := make(chan string, 1)
	release := make(chan struct{})
	defer close(release)
	if wedged.Generation != 1 {
		t.Fatalf("the supervisor spawned generation %d, but the fake sidecar says 1", wedged.Generation)
	}
	runtime := startFakeAppRuntime(t, d, enterOnceThenBlock(entered, release))

	failed := make(chan error, 1)
	go func() {
		failed <- d.deliverAppEvent(context.Background(), "hog", appEvent("ticket.created", "tk-1", 1))
	}()
	if app := <-entered; app != "hog" {
		t.Fatalf("first handler in was %s, want hog", app)
	}
	runtime.freezeLoop()

	if err := <-failed; err == nil {
		t.Fatal("a dispatch into a frozen runtime reported success")
	}
	waitFor(t, "the supervisor to replace the generation the timeout ended", func() bool {
		snapshot, ok := d.appRuntimeSnapshot()
		return ok && snapshot.Generation > wedged.Generation && snapshot.Running
	})
	replacement, _ := d.appRuntimeSnapshot()
	if replacement.LastExit == nil {
		t.Fatal("the replacement carries no exit for the generation that was ended")
	}
}

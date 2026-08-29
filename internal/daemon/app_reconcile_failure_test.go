package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func reconcilingApp(t *testing.T, d *Daemon, name string) store.AppVersion {
	t.Helper()
	installApp(t, d, name, subscribing("ticket.*"))
	second := subscribing("ticket.*")
	second.Description = "the version that owes a rebuild"
	return installApp(t, d, name, second)
}

func appReconcilePreDrain(t *testing.T, d *Daemon, name string) error {
	t.Helper()
	return d.appPreDrain(name)(context.Background(), bus.Consumer{Name: apps.ConsumerName(name)}, nil)
}

func TestAReconcileInterruptedByARestartIsRepairedAndStaysOwed(t *testing.T) {
	d := newAppDaemon(t)
	version := reconcilingApp(t, d, "greeter")

	claim, err := d.store.AppReconcilePending("greeter")
	if err != nil || len(claim.Requests) != 1 {
		t.Fatalf("claim = %+v, %v", claim, err)
	}
	if _, err := d.store.StartAppInvocation(store.AppInvocation{
		AppName: "greeter", VersionID: version.ID, Kind: store.AppInvocationKindReconcile,
		Handler: "reconcile", Status: store.AppInvocationStatusRunning, StartedAt: d.appNow(),
		ReconcileReason:  `{"causes":["version_changed"]}`,
		ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	}); err != nil {
		t.Fatalf("start the attempt a dying daemon leaves behind: %v", err)
	}

	running := appStatus(t, d, "greeter").AppStatusResult.Reconcile
	if running.State != appReconcileStateRunning || running.CurrentAttempt == nil {
		t.Fatalf("status while an attempt runs = %+v", running)
	}
	if running.CurrentAttempt.DurationMs != nil {
		t.Fatalf("a running attempt reported a duration: %+v", running.CurrentAttempt)
	}

	if err := d.repairInterruptedAppInvocations(); err != nil {
		t.Fatalf("startup repair: %v", err)
	}

	repaired, ok, err := d.store.LatestOwedAppReconcileInvocation("greeter")
	if err != nil || !ok || repaired.Status != store.AppInvocationStatusInterrupted {
		t.Fatalf("repaired attempt = %+v ok=%t err=%v", repaired, ok, err)
	}
	after, err := d.store.AppReconcilePending("greeter")
	if err != nil || after.ThroughRequestID != claim.ThroughRequestID || len(after.Requests) != 1 {
		t.Fatalf("interruption moved the request: %+v, %v", after, err)
	}
	status := appStatus(t, d, "greeter").AppStatusResult.Reconcile
	if status.State != appReconcileStateOwed || status.CurrentAttempt != nil {
		t.Fatalf("status after repair = %+v", status)
	}
	if status.Reason == nil || status.Reason.ThroughSeq != int(claim.ThroughSeq) {
		t.Fatalf("status after repair carries no fence: %+v", status.Reason)
	}
}

func TestAnInterruptedAttemptDoesNotAdvanceTheStallClock(t *testing.T) {
	d := newAppDaemon(t)
	version := reconcilingApp(t, d, "greeter")
	claim, err := d.store.AppReconcilePending("greeter")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.StartAppInvocation(store.AppInvocation{
		AppName: "greeter", VersionID: version.ID, Kind: store.AppInvocationKindReconcile,
		Handler: "reconcile", StartedAt: d.appNow(), ReconcileReason: `{}`,
		ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.repairInterruptedAppInvocations(); err != nil {
		t.Fatal(err)
	}
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("an interrupted attempt put the app on the auto-disable clock: %+v", stall)
	}
}

func TestAReconcileThatKeepsThrowingDisablesTheAppAndSaysSo(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	reconcilingApp(t, d, "greeter")
	runtime := startFakeAppRuntime(t, d, nil)
	runtime.reconcile = func(*fakeAppRuntime, appReconcileRequest) error {
		return errors.New("TypeError: snapshot.sessions is not iterable")
	}
	claim, err := d.store.AppReconcilePending("greeter")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		if err := appReconcilePreDrain(t, d, "greeter"); err == nil {
			t.Fatal("a throwing reconcile reported success")
		}
		clock.advance(3 * time.Minute)
		if !appEnabled(t, d, "greeter") {
			t.Fatalf("greeter was disabled after %d attempt(s) inside the window", i+1)
		}
	}
	status := appStatus(t, d, "greeter").AppStatusResult
	if status.Reconcile.LastError == nil || !strings.Contains(*status.Reconcile.LastError, "TypeError") {
		waitForCond(t, 5*time.Second, "the last reconcile failure to reach app status", func() bool {
			status = appStatus(t, d, "greeter").AppStatusResult
			return status.Reconcile.LastError != nil && strings.Contains(*status.Reconcile.LastError, "TypeError")
		})
	}
	if status.Stall == nil || status.Stall.Kind != appStallKindReconcile ||
		protocol.Deref(status.Stall.ThroughRequestID) != int(claim.ThroughRequestID) {
		t.Fatalf("stall = %+v, want the reconcile claim", status.Stall)
	}
	if status.Reconcile.LastError == nil || !strings.Contains(*status.Reconcile.LastError, "TypeError") {
		t.Fatalf("status does not carry the last failure: %+v", status.Reconcile)
	}

	clock.advance(4 * time.Minute)
	if err := appReconcilePreDrain(t, d, "greeter"); err == nil {
		t.Fatal("a throwing reconcile reported success")
	}

	if appEnabled(t, d, "greeter") {
		t.Fatal("greeter is still enabled after 15 minutes owing the same rebuild")
	}
	notes := appNotifications(t, d, notificationKindAppAutoDisabled)
	if len(notes) != 1 {
		t.Fatalf("auto-disable notifications = %d, want 1", len(notes))
	}
	if !strings.Contains(notes[0].Body, "attn app enable greeter") ||
		!strings.Contains(notes[0].Body, "remains owed") {
		t.Fatalf("the notification does not say what is owed or how to get back: %q", notes[0].Body)
	}
	after, err := d.store.AppReconcilePending("greeter")
	if err != nil || after.ThroughRequestID != claim.ThroughRequestID {
		t.Fatalf("the request moved when the app was disabled: %+v, %v", after, err)
	}
	attempts := 0
	for _, inv := range invocationsOf(t, d, "greeter") {
		if inv.Kind == store.AppInvocationKindReconcile {
			attempts++
			if inv.Status != store.AppInvocationStatusError || inv.ThroughRequestID != claim.ThroughRequestID {
				t.Fatalf("attempt = %+v", inv)
			}
		}
	}
	if attempts < 5 {
		t.Fatalf("recorded attempts = %d, want one per try", attempts)
	}
}

func TestACommandIsRefusedByNameWhileAReconcileIsOwed(t *testing.T) {
	d := newAppDaemon(t)
	manifest := subscribing("ticket.*")
	manifest.Commands = []appbuild.Command{{Name: "refresh"}}
	installApp(t, d, "greeter", manifest)
	second := manifest
	second.Description = "the version that owes a rebuild"
	installApp(t, d, "greeter", second)
	runtime := startFakeAppRuntime(t, d, nil)
	runtime.reconcile = func(*fakeAppRuntime, appReconcileRequest) error {
		return errors.New("this rebuild never succeeds")
	}

	result := newAppCommandCaller().invoke(t, d, "greeter", "refresh", "")
	if result.Success {
		t.Fatal("a command ran across the reconcile fence")
	}
	if protocol.Deref(result.ErrorCode) != protocol.ErrorCodeReconcileOwed {
		t.Fatalf("refusal code = %q, want %q", protocol.Deref(result.ErrorCode), protocol.ErrorCodeReconcileOwed)
	}
	if result.Reconcile == nil || len(result.Reconcile.Causes) == 0 {
		t.Fatalf("the refusal carries no structured reason: %+v", result.Reconcile)
	}
	refusal := protocol.Deref(result.Error)
	if !strings.Contains(refusal, "greeter") || !strings.Contains(refusal, "rebuilding") {
		t.Fatalf("the refusal does not name the app and what it is doing: %q", refusal)
	}
	if strings.HasPrefix(refusal, protocol.ErrorCodeReconcileOwed) {
		t.Fatalf("the refusal leads with the wire code instead of saying what happened: %q", refusal)
	}
	if !strings.Contains(refusal, "attn app status greeter") {
		t.Fatalf("the refusal does not name where to look: %q", refusal)
	}

	if stall, ok := d.appStallSnapshot("greeter"); ok && stall.kind != appStallKindReconcile {
		t.Fatalf("a refused command put the app on the auto-disable clock: %+v", stall)
	}
}

func TestAGapWithNoHandlerDisablesTheAppWithoutMovingItsCursor(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribingWithoutReconcile("ticket.*"))
	seedAppConsumer(t, d, "greeter", true, 1)

	gap := &bus.Gap{Cursor: 1, Earliest: 4, Head: 6, Missed: 2}
	err := d.appPreDrain("greeter")(context.Background(),
		bus.Consumer{Name: apps.ConsumerName("greeter")}, gap)
	if err == nil || !strings.Contains(err.Error(), "does not declare reconcile") {
		t.Fatalf("pre-drain error = %v", err)
	}
	if appEnabled(t, d, "greeter") {
		t.Fatal("an app that cannot reconcile a gap was left enabled")
	}
	consumer, _, err := d.store.GetBusConsumer(apps.ConsumerName("greeter"))
	if err != nil || consumer.Cursor != 1 {
		t.Fatalf("cursor = %d, %v; a gap it cannot rebuild must not move it", consumer.Cursor, err)
	}
	notes := appNotifications(t, d, notificationKindAppAutoDisabled)
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "reconcile") {
		t.Fatalf("notifications = %+v", notes)
	}
	var missing []store.AppInvocation
	for _, inv := range invocationsOf(t, d, "greeter") {
		if inv.Handler == "missing_reconcile" {
			missing = append(missing, inv)
		}
	}
	if len(missing) != 1 || missing[0].Kind != store.AppInvocationKindReconcile {
		t.Fatalf("missing_reconcile invocations = %+v, want exactly one", missing)
	}

	status := appStatus(t, d, "greeter").AppStatusResult.Reconcile
	if status.State != appReconcileStateUnsupported || status.Reason == nil {
		t.Fatalf("status = %+v", status)
	}
}

func TestAVersionMoveIsRefusedWhenTheSubscribedVersionCannotReconcile(t *testing.T) {
	d := appApplyDaemon(t)
	legacy := `{"name":"greeter","attn_app_api":1,"entrypoint":"src/index.ts","subscribe":[{"events":["ticket.*"]}]}`
	firstHash := stageArtifact(t, d, "greeter", legacy, "export default {}")
	if resp := appApply(t, d, "greeter", firstHash, legacy); !resp.Ok {
		t.Fatalf("first apply: %v", protocol.Deref(resp.Error))
	}
	first, _, err := d.store.GetApp("greeter")
	if err != nil {
		t.Fatal(err)
	}

	secondHash := stageArtifact(t, d, "greeter", legacy, "export default {} // edited")
	resp := appApply(t, d, "greeter", secondHash, legacy)
	if resp.Ok {
		t.Fatal("a subscribed version with no reconcile handler was applied over an existing one")
	}
	message := protocol.Deref(resp.Error)
	for _, want := range []string{"greeter", "reconcile = true", "version 1"} {
		if !strings.Contains(message, want) {
			t.Fatalf("refusal %q does not contain %q", message, want)
		}
	}
	app, _, err := d.store.GetApp("greeter")
	if err != nil || app.CurrentVersionID != first.CurrentVersionID {
		t.Fatalf("the pointer moved despite the refusal: %+v, %v", app, err)
	}
	if claim, err := d.store.AppReconcilePending("greeter"); err != nil || len(claim.Requests) != 0 {
		t.Fatalf("a refused move left a request behind: %+v, %v", claim, err)
	}
}

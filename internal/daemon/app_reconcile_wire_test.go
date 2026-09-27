package daemon_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func rebuildingApp(name string) appbuild.Manifest {
	m := withCollections(subscriber(name, "ticket.*"), "switch")
	m.Reconcile = true
	m.Commands = []appbuild.Command{{Name: "refresh"}}
	return m
}

func rebuildingBundle(reconcile string) string {
	return `export default {
  subscriptions: { "ticket.*": (ev, ctx) => ctx.collections.marks.put("fact-" + ev.subject, { seq: ev.seq, version: ctx.version }) },
  commands: { refresh: () => "refreshed" },
  reconcile: async (reason, ctx) => {` + reconcile + `},
}
`
}

const recordReconcile = `
    const snapshot = await ctx.current.snapshot()
    await ctx.collections.marks.put("reconcile-" + reason.version, {
      reason: JSON.stringify(reason), asOfSeq: snapshot.asOfSeq, apps: snapshot.apps.map((a) => a.name),
    })`

func setAppEnabled(t *testing.T, cli *client.Client, name string, enabled bool) {
	t.Helper()
	if _, err := cli.AppSetEnabled(name, enabled); err != nil {
		t.Fatalf("set %s enabled=%t: %v", name, enabled, err)
	}
}

func fileTicket(t *testing.T, cli *client.Client, title string) string {
	t.Helper()
	ticket, err := cli.CreateTicket("planner", title, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return ticket.TicketID
}

func nextOfKind(t *testing.T, invocations <-chan protocol.AppInvocationInfo, kind, status string) protocol.AppInvocationInfo {
	t.Helper()
	for {
		if got := awaitAppInvocation(t, invocations); got.Kind == kind && got.Status == status {
			return got
		}
	}
}

func TestAVersionChangeReconcilesOnceAtTheFrozenCursorBeforeTheRetainedFacts(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	first := applyRunningApp(t, cli, rebuildingApp("greeter"), rebuildingBundle(recordReconcile+"\n// v1\n"))
	setAppEnabled(t, cli, "greeter", false)
	frozen := appStatus(t, cli, "greeter").App.Consumer.Cursor
	retained := fileTicket(t, cli, "Price the order")
	second := applyRunningApp(t, cli, rebuildingApp("greeter"), rebuildingBundle(recordReconcile+"\n// v2\n"))
	third := applyRunningApp(t, cli, rebuildingApp("greeter"), rebuildingBundle(recordReconcile+"\n// v3\n"))
	marks := watchAppDocs(t, w, "greeter", "marks")
	invocations := watchAppInvocations(t, w, "greeter")

	setAppEnabled(t, cli, "greeter", true)

	rebuilt := nextOfKind(t, invocations, "reconcile", "ok")
	handled := nextOfKind(t, invocations, "subscription", "ok")
	if protocol.Deref(handled.EventSubject) != retained {
		t.Errorf("after the rebuild greeter handled %+v, want the retained ticket %s", handled, retained)
	}
	record := marks.await(fmt.Sprintf("reconcile-%d", third.VersionID))
	want := fmt.Sprintf(`{"causes":["version_changed"],"version":%d,"throughSeq":%d,"previousVersions":[%d,%d]}`, third.VersionID, frozen, first.VersionID, second.VersionID)
	if record["reason"] != want {
		t.Errorf("the reconcile was given %v, want %s", record["reason"], want)
	}
	if asOf, _ := record["asOfSeq"].(float64); int(asOf) < frozen || fmt.Sprint(record["apps"]) != "[greeter]" {
		t.Errorf("the reconcile's current snapshot = %v, want it at or past seq %d and listing greeter", record, frozen)
	}
	if fact := marks.await("fact-" + retained); fact["version"] != float64(third.VersionID) {
		t.Errorf("the retained fact was handled as %v, want by version %d", fact, third.VersionID)
	}
	status := appStatus(t, cli, "greeter")
	reconciles := 0
	for _, invocation := range status.Recent {
		if invocation.Kind == "reconcile" {
			reconciles++
		}
	}
	if reconciles != 1 || status.Reconcile.State != "idle" || rebuilt.Reconcile == nil {
		t.Errorf("after the rebuild: %d reconcile invocation(s), state %q; want one and idle", reconciles, status.Reconcile.State)
	}
}

func TestReEnablingAnAppDeliversWhatItMissedInOrderWithoutARebuild(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, rebuildingApp("greeter"), rebuildingBundle(recordReconcile))
	setAppEnabled(t, cli, "greeter", false)
	missed := []string{fileTicket(t, cli, "Price the order"), fileTicket(t, cli, "Ship the order")}
	invocations := watchAppInvocations(t, w, "greeter")

	setAppEnabled(t, cli, "greeter", true)

	for _, want := range missed {
		got := awaitAppInvocation(t, invocations)
		for protocol.Deref(got.EventName) != "ticket.created" {
			got = awaitAppInvocation(t, invocations)
		}
		if got.Kind != "subscription" || got.Status != "ok" || protocol.Deref(got.EventSubject) != want {
			t.Fatalf("after re-enabling, greeter ran %+v, want the missed ticket %s next", got, want)
		}
	}
	if state := appStatus(t, cli, "greeter").Reconcile.State; state != "idle" {
		t.Errorf("reconcile state after re-enabling = %q, want idle", state)
	}
}

func TestAReconcileThatThrowsHoldsFactsAndCommandsBackAndIsRetriedWithTheSameReason(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, rebuildingApp("greeter"), rebuildingBundle(""))
	marks := watchAppDocs(t, w, "greeter", "marks")
	invocations := watchAppInvocations(t, w, "greeter")
	applyRunningApp(t, cli, rebuildingApp("greeter"), rebuildingBundle(`
    if (!(await ctx.collections.switch.get("fixed"))) throw new TypeError("snapshot.sessions is not iterable")`))
	held := fileTicket(t, cli, "Price the order")

	thrown := nextOfKind(t, invocations, "reconcile", "error")
	refused := requestAppCommand(w.App(), "greeter", "refresh", "")
	status := appStatus(t, cli, "greeter")
	if status.Reconcile.State != "owed" || !strings.Contains(protocol.Deref(status.Reconcile.LastError), "TypeError") {
		t.Errorf("reconcile status after a throw = %+v, want owed with what threw", status.Reconcile)
	}
	if status.Stall == nil || status.Stall.Kind != "reconcile" || protocol.Deref(status.Stall.ThroughRequestID) != protocol.Deref(thrown.ThroughRequestID) {
		t.Errorf("stall after a throw = %+v, want the clock on the owed rebuild", status.Stall)
	}
	if refused.Success || protocol.Deref(refused.ErrorCode) != protocol.ErrorCodeReconcileOwed || refused.Reconcile == nil ||
		fmt.Sprint(refused.Reconcile.Causes) != "[version_changed]" || strings.HasPrefix(protocol.Deref(refused.Error), protocol.ErrorCodeReconcileOwed) {
		t.Errorf("a command while the rebuild is owed = %+v, want a refusal coded %s with the reason", refused, protocol.ErrorCodeReconcileOwed)
	}
	requireMentions(t, "the command refusal", protocol.Deref(refused.Error), "greeter", "rebuilding", "attn app status greeter")

	if _, err := cli.DocPut("app/greeter", "switch", "fixed", `{}`, nil); err != nil {
		t.Fatal(err)
	}
	passed := nextOfKind(t, invocations, "reconcile", "ok")
	if fmt.Sprint(*passed.Reconcile) != fmt.Sprint(*thrown.Reconcile) {
		t.Errorf("the retry was given %+v, want the reason the throw had: %+v", *passed.Reconcile, *thrown.Reconcile)
	}
	if handled := nextOfKind(t, invocations, "subscription", "ok"); protocol.Deref(handled.EventSubject) != held {
		t.Errorf("after the rebuild greeter handled %+v, want the held ticket %s", handled, held)
	}
	marks.await("fact-" + held)
	if answered := requestAppCommand(w.App(), "greeter", "refresh", ""); !answered.Success {
		t.Errorf("a command after the rebuild = %+v, want it run", answered)
	}
}

func TestAReconcileThatKeepsThrowingDisablesTheAppAndLeavesTheRebuildOwed(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	t.Setenv("ATTN_APP_AUTO_DISABLE_STALL", "500ms")
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	applyRunningApp(t, cli, rebuildingApp("greeter"), rebuildingBundle(""))
	invocations := watchAppInvocations(t, w, "greeter")
	applyRunningApp(t, cli, rebuildingApp("greeter"), rebuildingBundle(`throw new TypeError("snapshot.sessions is not iterable")`))
	first := nextOfKind(t, invocations, "reconcile", "error")

	awaitAppEnabled(app, "greeter", false)

	notes := awaitAppNotifications(app, "app_auto_disabled")
	if len(notes) != 1 {
		t.Fatalf("auto-disable notifications = %+v, want one", notes)
	}
	requireMentions(t, "the notification", notes[0].Body, "remains owed", "attn app enable greeter")
	if !regexp.MustCompile(`for [1-9][0-9]*s across [2-9][0-9]* attempts`).MatchString(notes[0].Body) {
		t.Errorf("the notification does not say how long attn tried: %s", notes[0].Body)
	}
	requireMentions(t, "the notification detail", notes[0].Detail, "TypeError")
	status := appStatus(t, cli, "greeter")
	if status.Reconcile.State != "owed" || status.Reconcile.Reason == nil || status.Reconcile.Reason.ThroughSeq != first.Reconcile.ThroughSeq {
		t.Errorf("reconcile after the disable = %+v, want still owed through seq %d", status.Reconcile, first.Reconcile.ThroughSeq)
	}
	for _, invocation := range status.Recent {
		if invocation.Kind == "reconcile" && (invocation.Status != "error" || protocol.Deref(invocation.ThroughRequestID) != protocol.Deref(first.ThroughRequestID)) {
			t.Errorf("reconcile attempt %+v, want every attempt an error on request %d", invocation, protocol.Deref(first.ThroughRequestID))
		}
	}
}

func TestAReconcileInterruptedByARestartStaysOwedAndIsNotChargedToTheApp(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, rebuildingApp("greeter"), rebuildingBundle(""))
	marks := watchAppDocs(t, w, "greeter", "marks")
	applyRunningApp(t, cli, rebuildingApp("greeter"), rebuildingBundle(`
    await ctx.collections.marks.put("entered", {})
    await new Promise(() => {})`))
	marks.await("entered")
	running := appStatus(t, cli, "greeter").Reconcile
	if running.State != "running" || running.CurrentAttempt == nil || running.CurrentAttempt.DurationMs != nil || running.Reason == nil {
		t.Fatalf("reconcile while the attempt runs = %+v, want running with an open attempt", running)
	}

	w.restart()
	status := appStatus(t, w.Client(), "greeter")
	if status.Reconcile.State != "owed" && status.Reconcile.State != "running" || status.Reconcile.Reason == nil || status.Reconcile.Reason.ThroughSeq != running.Reason.ThroughSeq {
		t.Errorf("reconcile after the restart = %+v, want the rebuild through seq %d still owed", status.Reconcile, running.Reason.ThroughSeq)
	}
	interrupted := false
	for _, invocation := range status.Recent {
		if invocation.ID == running.CurrentAttempt.ID {
			interrupted = invocation.Status == "interrupted"
		}
	}
	if !interrupted || status.Stall != nil {
		t.Errorf("after the restart the attempt was %+v with stall %+v; want it interrupted and the app off the auto-disable clock", status.Recent, status.Stall)
	}
}

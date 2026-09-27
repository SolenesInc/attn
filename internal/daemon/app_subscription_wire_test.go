package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAppHandlesEachFactItSubscribesToWithItsMostSpecificSubscription(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	greeter := subscriber("greeter", "ticket.*", "ticket.created")
	version := applyRunningApp(t, cli, greeter, `
const mark = (handler) => async (ev, ctx) => {
  await ctx.collections.marks.put(ev.name, { handler, subject: ev.subject, seq: ev.seq, version: ctx.version })
}
export default { subscriptions: { "ticket.*": mark("ticket.*"), "ticket.created": mark("ticket.created") } }
`)
	applyRunningApp(t, cli, subscriber("auditor", "ticket.*"), `export default { subscriptions: { "ticket.*": () => {} } }`)
	marks := watchAppDocs(t, w, "greeter", "marks")
	invocations := watchAppInvocations(t, w, "greeter")

	ticket, err := cli.CreateTicket("planner", "Price the order", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CommentTicket("planner", ticket.TicketID, "priced"); err != nil {
		t.Fatal(err)
	}

	created, commented := marks.await("ticket.created"), marks.await("ticket.commented")
	if created["handler"] != "ticket.created" || created["subject"] != ticket.TicketID || created["version"] != float64(version.VersionID) {
		t.Errorf("ticket.created was handled as %v, want by its exact subscription on version %d", created, version.VersionID)
	}
	if commented["handler"] != "ticket.*" {
		t.Errorf("ticket.commented was handled as %v, want by the wildcard", commented)
	}
	want := map[string]struct {
		handler string
		seq     int
	}{
		"ticket.created":   {"subscribe:ticket.created", int(created["seq"].(float64))},
		"ticket.commented": {"subscribe:ticket.*", int(commented["seq"].(float64))},
	}
	for range 2 {
		got := awaitAppInvocation(t, invocations)
		name := protocol.Deref(got.EventName)
		expected, ok := want[name]
		if !ok {
			t.Fatalf("greeter's watch streamed unexpected or repeated %s: %+v", name, got)
		}
		delete(want, name)
		if got.Status != "ok" || got.Handler != expected.handler ||
			protocol.Deref(got.EventSubject) != ticket.TicketID || protocol.Deref(got.EventSeq) != expected.seq ||
			got.VersionID != version.VersionID {
			t.Errorf("greeter's watch streamed %+v, want its own ok %s at seq %d on version %d", got, name, expected.seq, version.VersionID)
		}
	}
}

func TestAThrownHandlerIsRecordedWithItsStackAndItsFactRetriedUntilItPasses(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, settling(withCollections(subscriber("greeter", "ticket.*"), "switch")), `
export default {
  subscriptions: { "ticket.*": async (ev, ctx) => {
    if (!(await ctx.collections.switch.get("fixed"))) throw new TypeError("cannot read properties of undefined")
    await ctx.collections.marks.put(ev.subject, { seq: ev.seq })
  } },
  commands: { settle: () => {} },
}
`)
	marks := watchAppDocs(t, w, "greeter", "marks")
	invocations := watchAppInvocations(t, w, "greeter")

	ticket, err := cli.CreateTicket("planner", "Price the order", "", "")
	if err != nil {
		t.Fatal(err)
	}
	thrown := awaitAppInvocation(t, invocations)
	if thrown.Status != "error" || !strings.Contains(protocol.Deref(thrown.Error), "TypeError") || !strings.Contains(protocol.Deref(thrown.Error), "\n    at ") {
		t.Fatalf("the throw was recorded as %+v, want an error carrying the handler's stack", thrown)
	}
	settleApp(w.App(), "greeter")
	stall := appStatus(t, cli, "greeter").Stall
	if stall == nil || stall.Kind != "subscription" || protocol.Deref(stall.EventName) != "ticket.created" ||
		protocol.Deref(stall.EventSeq) != protocol.Deref(thrown.EventSeq) || stall.Attempts < 1 || !strings.Contains(stall.LastError, "TypeError") {
		t.Fatalf("the status stall = %+v, want the clock on ticket.created at seq %d and what threw", stall, protocol.Deref(thrown.EventSeq))
	}
	if disablesAt, err := time.Parse(time.RFC3339Nano, stall.DisablesAt); err != nil || time.Until(disablesAt) <= 14*time.Minute || time.Until(disablesAt) > 15*time.Minute {
		t.Errorf("the stall disables at %q, want fifteen minutes after the first failure", stall.DisablesAt)
	}

	if _, err := cli.DocPut("app/greeter", "switch", "fixed", `{}`, nil); err != nil {
		t.Fatal(err)
	}
	passed := nextOfKind(t, invocations, "subscription", "ok")
	if protocol.Deref(passed.EventSeq) != protocol.Deref(thrown.EventSeq) {
		t.Errorf("the retry handled seq %d, want the fact that threw, seq %d", protocol.Deref(passed.EventSeq), protocol.Deref(thrown.EventSeq))
	}
	marks.await(ticket.TicketID)
	settleApp(w.App(), "greeter")
	if status := appStatus(t, cli, "greeter"); status.Stall != nil || !status.App.Consumer.Enabled {
		t.Errorf("after the retry passed: stall %+v, consumer %+v", status.Stall, status.App.Consumer)
	}
}

func TestARuntimeThatDiesMidHandlerIsNotChargedToTheApp(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, subscriber("greeter", "ticket.*"), `
export default { subscriptions: { "ticket.*": async (ev, ctx) => {
  if (!(await ctx.collections.marks.get("died"))) {
    await ctx.collections.marks.put("died", {})
    process.exit(1)
  }
  await ctx.collections.marks.put(ev.subject, {})
} } }
`)
	marks := watchAppDocs(t, w, "greeter", "marks")
	invocations := watchAppInvocations(t, w, "greeter")

	ticket, err := cli.CreateTicket("planner", "Price the order", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if died := awaitAppInvocation(t, invocations); died.Status != "runtime_error" {
		t.Fatalf("a delivery whose runtime died = %+v, want a runtime_error", died)
	}
	if stall := appStatus(t, cli, "greeter").Stall; stall != nil {
		t.Errorf("the runtime dying put greeter on the auto-disable clock: %+v", stall)
	}
	awaitInvocationWith(t, invocations, "ok")
	marks.await(ticket.TicketID)
}

func TestAnAppRemovedWhileItsHandlerRunsIsRemovedAtOnce(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, subscriber("greeter", "ticket.*"), `
export default { subscriptions: { "ticket.*": async (ev, ctx) => {
  await ctx.collections.marks.put("entered", {})
  await new Promise(() => {})
} } }
`)
	marks := watchAppDocs(t, w, "greeter", "marks")
	if _, err := cli.CreateTicket("planner", "Price the order", "", ""); err != nil {
		t.Fatal(err)
	}
	marks.await("entered")

	removed := make(chan *protocol.AppRemoveResult, 1)
	go func() {
		result, err := cli.AppRemove("greeter")
		if err != nil {
			t.Errorf("remove: %v", err)
		}
		removed <- result
	}()
	select {
	case result := <-removed:
		if result != nil && !result.ConsumerRemoved {
			t.Errorf("remove = %+v, want the consumer gone", result)
		}
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("removing an app waited on its running handler for %s", fakeagent.HangGuard)
	}
	if got := appNames(t, cli); got != "" {
		t.Errorf("apps after the removal = %q", got)
	}
}

func TestANewVersionsCodeRunsFromItsNextDispatch(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	manifest := subscriber("greeter", "ticket.*")
	manifest.Reconcile = true
	bundle := func(code string) string {
		return `export default {
  subscriptions: { "ticket.*": (ev, ctx) => ctx.collections.marks.put(ev.subject, { version: ctx.version, code: "` + code + `" }) },
  reconcile: () => {},
}
`
	}
	first := applyRunningApp(t, cli, manifest, bundle("one"))
	marks := watchAppDocs(t, w, "greeter", "marks")
	before, err := cli.CreateTicket("planner", "Price the order", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := marks.await(before.TicketID); got["version"] != float64(first.VersionID) || got["code"] != "one" {
		t.Fatalf("the first version handled the fact as %v", got)
	}

	second := applyRunningApp(t, cli, manifest, bundle("two"))
	after, err := cli.CreateTicket("planner", "Ship the order", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := marks.await(after.TicketID); got["version"] != float64(second.VersionID) || got["code"] != "two" {
		t.Errorf("after applying version %d the next fact was handled as %v, want the new version's code", second.VersionID, got)
	}
}

func TestARuntimeReachesOnlyItsAppsDeclaredCollectionsAndOnlyWhileTheHandlerRuns(t *testing.T) {
	runtime := testworld.NewFakeAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, withCollections(appbuild.Manifest{Name: "neighbour", Subscribe: []appbuild.Subscribe{{Events: []string{"pr.*"}}}}, "secrets"), "export default {}\n")
	applyRunningApp(t, cli, withCollections(appbuild.Manifest{Name: "greeter", Subscribe: []appbuild.Subscribe{{Events: []string{"ticket.*"}}}}, "seen"), "export default {}\n")
	invocations := watchAppInvocations(t, w, "greeter")
	if _, err := cli.AppRuntimeRestart(); err != nil {
		t.Fatal(err)
	}
	conn := runtime.Connect(w.DialUnix)

	if _, err := cli.CreateTicket("planner", "Price the order", "", ""); err != nil {
		t.Fatal(err)
	}
	dispatch, release := conn.HoldDispatch()
	if _, refused := conn.TryCall("app.collection.put", map[string]any{"dispatch": dispatch.Dispatch, "collection": "seen", "id": "tk", "body": map[string]any{"note": "mine"}}); refused != "" {
		t.Fatalf("greeter could not write its own collection: %s", refused)
	}
	_, refused := conn.TryCall("app.collection.get", map[string]any{"dispatch": dispatch.Dispatch, "collection": "secrets", "id": "anything"})
	requireMentions(t, "reaching neighbour's collection", refused, "did not declare a collection", "secrets")
	release()
	awaitInvocationWith(t, invocations, "ok")

	_, late := conn.TryCall("app.collection.get", map[string]any{"dispatch": dispatch.Dispatch, "collection": "seen", "id": "tk"})
	requireMentions(t, "a call after the handler returned", late, "after that handler returned")
	if doc, err := cli.DocGet("app/greeter", "seen", "tk"); err != nil || !doc.Found {
		t.Errorf("greeter's own write = %+v, %v; want it in app/greeter", doc, err)
	}
}

func TestAnAppsLogsShowWhatItsHandlersPrintedAndTheRuntimesShowEverything(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, subscriber("greeter", "ticket.created"), `
export default { subscriptions: { "ticket.created": async (ev, ctx) => {
  console.log("hello from greeter")
  console.error("and a warning")
  await ctx.collections.marks.put(ev.subject, {})
} } }
`)
	marks := watchAppDocs(t, w, "greeter", "marks")
	marks.await(fileTicket(t, cli, "Price the order"))

	own, err := cli.AppLogs("greeter", 0)
	if err != nil || strings.Join(own.Lines, "|") != "hello from greeter|and a warning" {
		t.Errorf("greeter's logs = %+v, %v; want its two lines, untagged", own, err)
	}
	whole, err := cli.AppLogs("runtime", 0)
	if err != nil {
		t.Fatal(err)
	}
	requireMentions(t, "the runtime's logs", strings.Join(whole.Lines, "\n"), "[runtime] app runtime ready", "[app greeter] hello from greeter")
}

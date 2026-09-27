package daemon_test

import (
	"regexp"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const stuckUntilFixed = `
export default {
  subscriptions: { "ticket.*": async (ev, ctx) => {
    if (!(await ctx.collections.switch.get("fixed"))) throw new TypeError("undefined is not a function")
  } },
  commands: { settle: () => {} },
}
`

func TestAnAppStuckOnOneFactIsDisabledAndSaysWhatFailedAndForHowLong(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	t.Setenv("ATTN_APP_AUTO_DISABLE_STALL", "500ms")
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	applyRunningApp(t, cli, withCollections(subscriber("greeter", "ticket.*"), "switch"), stuckUntilFixed)
	fileTicket(t, cli, "Price the order")

	awaitAppEnabled(app, "greeter", false)

	notes := awaitAppNotifications(app, "app_auto_disabled")
	if len(notes) != 1 || notes[0].Severity != protocol.NotificationSeverityWarning {
		t.Fatalf("auto-disable notifications = %+v, want one warning", notes)
	}
	requireMentions(t, "the notification", notes[0].Body, "ticket.created", "attn app enable greeter")
	if !regexp.MustCompile(`for [1-9][0-9]*s across [2-9][0-9]* attempts`).MatchString(notes[0].Body) {
		t.Errorf("the notification does not say how long attn tried: %s", notes[0].Body)
	}
	requireMentions(t, "the notification detail", notes[0].Detail, "TypeError")
	if status := appStatus(t, cli, "greeter"); status.Stall != nil || status.App.Consumer.Enabled {
		t.Errorf("after the auto-disable: stall %+v, consumer %+v; want it disabled and off the clock", status.Stall, status.App.Consumer)
	}
}

func TestAnAppThatPassesOnRetryIsNeverDisabledHoweverSlowlyItPasses(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	t.Setenv("ATTN_APP_AUTO_DISABLE_STALL", "500ms")
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, settling(subscriber("greeter", "ticket.created")), `
export default {
  subscriptions: { "ticket.created": async (ev, ctx) => {
    if (!(await ctx.collections.marks.get(ev.subject))) {
      await ctx.collections.marks.put(ev.subject, {})
      throw new Error("the first try at each ticket fails")
    }
    await new Promise((resolve) => setTimeout(resolve, 700))
  } },
  commands: { settle: () => {} },
}
`)
	invocations := watchAppInvocations(t, w, "greeter")

	for _, title := range []string{"Price the order", "Ship the order"} {
		ticket := fileTicket(t, cli, title)
		for _, want := range []string{"error", "ok"} {
			if got := awaitAppInvocation(t, invocations); got.Status != want || protocol.Deref(got.EventSubject) != ticket {
				t.Fatalf("greeter ran %+v, want %s on %s", got, want, ticket)
			}
		}
	}
	settleApp(w.App(), "greeter")
	if status := appStatus(t, cli, "greeter"); status.Stall != nil || !status.App.Consumer.Enabled {
		t.Errorf("an app that passed every retry: stall %+v, consumer %+v; want it enabled and off the clock", status.Stall, status.App.Consumer)
	}
}

func TestReEnablingAnAutoDisabledAppGivesItAFreshWindowAndResumesDelivery(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	t.Setenv("ATTN_APP_AUTO_DISABLE_STALL", "500ms")
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	applyRunningApp(t, cli, settling(withCollections(subscriber("greeter", "ticket.*"), "switch")), stuckUntilFixed)
	invocations := watchAppInvocations(t, w, "greeter")
	stuck := fileTicket(t, cli, "Price the order")
	awaitAppEnabled(app, "greeter", false)
	lastBefore := appStatus(t, cli, "greeter").Recent[0].ID

	setAppEnabled(t, cli, "greeter", true)
	for got := awaitAppInvocation(t, invocations); got.ID <= lastBefore; got = awaitAppInvocation(t, invocations) {
	}
	settleApp(app, "greeter")
	status := appStatus(t, cli, "greeter")
	if !status.App.Consumer.Enabled || status.Stall == nil || status.Stall.Attempts != 1 {
		t.Fatalf("the first failure after re-enabling left consumer %+v, stall %+v; want it enabled on a fresh clock", status.App.Consumer, status.Stall)
	}

	if _, err := cli.DocPut("app/greeter", "switch", "fixed", `{}`, nil); err != nil {
		t.Fatal(err)
	}
	if passed := nextOfKind(t, invocations, "subscription", "ok"); protocol.Deref(passed.EventSubject) != stuck {
		t.Errorf("after the fix greeter handled %+v, want the ticket it was stuck on", passed)
	}
	settleApp(app, "greeter")
	if status := appStatus(t, cli, "greeter"); status.Stall != nil || !status.App.Consumer.Enabled {
		t.Errorf("after the fix: stall %+v, consumer %+v", status.Stall, status.App.Consumer)
	}
}

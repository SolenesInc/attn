package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAppWhoseStrayErrorsKeepCrashingTheRuntimeIsDisabledAndItsNeighbourIsNot(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	applyRunningApp(t, cli, subscriber("leaker", "ticket.created"), `
export default { subscriptions: { "ticket.created": () => {
  setTimeout(() => { throw new TypeError("fetch failed") }, 0)
} } }
`)
	applyRunningApp(t, cli, subscriber("neighbour", "ticket.created"), `export default { subscriptions: { "ticket.created": () => {} } }`)

	for _, title := range []string{"Price the order", "Ship the order", "Bill the order"} {
		fileTicket(t, cli, title)
	}
	awaitAppEnabled(app, "leaker", false)

	notes := awaitAppNotifications(app, "app_auto_disabled")
	if len(notes) != 1 || notes[0].SourceID != "leaker" {
		t.Fatalf("auto-disable notifications = %+v, want one for leaker", notes)
	}
	requireMentions(t, "the notification", notes[0].Body, "crashed the shared app runtime 3 times", "uncaughtException", "attn app enable leaker")
	requireMentions(t, "the notification detail", notes[0].Detail, "fetch failed")
	if status := appStatus(t, cli, "neighbour"); !status.App.Consumer.Enabled {
		t.Errorf("neighbour shared the crashing runtime and was disabled: %+v", status.App.Consumer)
	}
}

func TestACrashNamingNoAppChargesNobodyAndReEnablingClearsAnAppsStrikes(t *testing.T) {
	runtime := testworld.NewFakeAppRuntime(t)
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	applySubscribedApp(t, cli, "leaker")
	if _, err := cli.AppRuntimeRestart(); err != nil {
		t.Fatal(err)
	}
	conn := runtime.Connect(w.DialUnix)
	crash := func(name string) {
		conn.Call("app_runtime.crashed", map[string]any{"app": name, "kind": "unhandledRejection", "error": "TypeError: fetch failed"})
	}

	for range 6 {
		crash("")
	}
	crash("leaker")
	crash("leaker")
	if status := appStatus(t, cli, "leaker"); !status.App.Consumer.Enabled {
		t.Fatalf("leaker was disabled by crashes that named no app: %+v", status.App.Consumer)
	}
	setAppEnabled(t, cli, "leaker", false)
	setAppEnabled(t, cli, "leaker", true)
	crash("leaker")
	crash("leaker")
	if status := appStatus(t, cli, "leaker"); !status.App.Consumer.Enabled {
		t.Fatalf("re-enabling left leaker's earlier strikes counting: %+v", status.App.Consumer)
	}
	crash("leaker")
	awaitAppEnabled(app, "leaker", false)
}

package daemon_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func busStatus(t *testing.T, app *testworld.Peer) protocol.BusStatusResultMessage {
	t.Helper()
	id := uuid.NewString()
	status := testworld.Request(app, protocol.BusStatusGetMessage{Cmd: protocol.CmdBusStatusGet, RequestID: id},
		protocol.EventBusStatusResult, func(m protocol.BusStatusResultMessage) bool { return m.RequestID == id })
	if !status.Success {
		t.Fatalf("bus status failed: %s", protocol.Deref(status.Error))
	}
	return status
}

func producedBy(status protocol.BusStatusResultMessage, name string) int {
	for _, p := range status.Producers {
		if p.Name == name {
			return p.Events
		}
	}
	return 0
}

func consumer(t *testing.T, status protocol.BusStatusResultMessage, name string) protocol.BusConsumerStatus {
	t.Helper()
	for _, c := range status.Consumers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("bus status lists no consumer %s: %+v", name, status.Consumers)
	return protocol.BusConsumerStatus{}
}

func TestAConsumerKeepsItsCursorAndItsKillSwitchAcrossARestart(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	defineRequests(t, cli, gateNS)
	applyDeclaration(t, cli, "history", subscribedApp("history"), "only")
	put(t, cli, gateNS, "a", `{}`)
	killed := testworld.Request(app, protocol.BusSetConsumerEnabledMessage{
		Cmd: protocol.CmdBusSetConsumerEnabled, RequestID: "kill", Consumer: "garden-seed-bells", Enabled: false,
	}, protocol.EventBusSetConsumerEnabledResult, func(m protocol.BusSetConsumerEnabledResultMessage) bool { return m.RequestID == "kill" })
	if !killed.Success {
		t.Fatalf("disabling garden-seed-bells: %s", protocol.Deref(killed.Error))
	}
	put(t, cli, gateNS, "b", `{}`)
	before := busStatus(t, app)

	w.restart()
	after := busStatus(t, w.App())
	for _, name := range []string{"garden-seed-bells", "app:history"} {
		was, is := consumer(t, before, name), consumer(t, after, name)
		if is.Cursor != was.Cursor || is.Enabled != was.Enabled {
			t.Errorf("%s restarted at cursor %d (enabled=%t), was at %d (enabled=%t)", name, is.Cursor, is.Enabled, was.Cursor, was.Enabled)
		}
	}
	if bells := consumer(t, after, "garden-seed-bells"); bells.Enabled || bells.Lag == 0 {
		t.Errorf("the killed consumer came back as %+v; want it still disabled, behind the writes made after the kill", bells)
	}
}

func subscribedApp(name string) string {
	return `{"name":"` + name + `","attn_app_api":1,"entrypoint":"src/index.ts","subscribe":[{"events":["document.changed"]}]}`
}

func TestAnHourLaterTheLogKeepsWhatAnInstalledAppHasNotReadAndCountsEachWindow(t *testing.T) {
	t.Setenv("ATTN_BUS_RETENTION", "30m")
	inBubble(t, func(t *testing.T, w *world) {
		cli, app := w.Client(), w.App()
		defineRequests(t, cli, gateNS)
		put(t, cli, gateNS, "old", `{}`)
		applyDeclaration(t, cli, "ghost", subscribedApp("ghost"), "only")
		applyDeclaration(t, cli, "history", subscribedApp("history"), "only")
		put(t, cli, gateNS, "between", `{}`)
		applyDeclaration(t, cli, "archive", subscribedApp("archive"), "only")
		if _, err := cli.AppSetEnabled("archive", false); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.AppRemove("ghost"); err != nil {
			t.Fatal(err)
		}
		unread := put(t, cli, gateNS, "unread", `{}`)
		w.advance(45 * time.Minute)
		young := put(t, cli, gateNS, "young", `{}`)
		installed := busStatus(t, app)
		archive, history := consumer(t, installed, "app:archive"), consumer(t, installed, "app:history")
		if archive.Enabled || !history.Enabled || history.Cursor >= archive.Cursor || archive.Cursor >= unread.Seq {
			t.Fatalf("before the trim archive = %+v and history = %+v; want the enabled history lowest and both behind the unread write at %d", archive, history, unread.Seq)
		}

		w.advance(16 * time.Minute)
		trimmed := busStatus(t, app)
		if trimmed.Earliest != history.Cursor+1 || trimmed.Head < young.Seq {
			t.Fatalf("after the hourly trim the log spans %d..%d; want it to start right past the lagging history's cursor %d and still reach %d", trimmed.Earliest, trimmed.Head, history.Cursor, young.Seq)
		}
		if lagging := consumer(t, trimmed, "app:history"); lagging.Cursor != history.Cursor || lagging.Lag == 0 {
			t.Fatalf("history after the trim = %+v, want it still behind at %d", lagging, history.Cursor)
		}
		for _, p := range trimmed.Producers {
			if p.Name == "document.changed" && (p.Events != 3 || p.RecentPerHour != 1 || p.SustainedPerHour != 3.0/6 || p.BaselinePerHour != 3.0/24) {
				t.Errorf("document.changed = %+v; want the between, unread and young writes kept, only the young one inside the last hour", p)
			}
		}

		if _, err := cli.AppRemove("history"); err != nil {
			t.Fatal(err)
		}
		w.advance(time.Hour)
		if retrimmed := busStatus(t, app); retrimmed.Earliest != archive.Cursor+1 {
			t.Fatalf("after history left and another hourly trim the log starts at %d; want it right past the disabled archive's cursor %d", retrimmed.Earliest, archive.Cursor)
		}
	})
}

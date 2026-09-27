package daemon_test

import (
	"testing"

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
	for _, name := range []string{"garden-seed-bells"} {
		was, is := consumer(t, before, name), consumer(t, after, name)
		rewound := is.Cursor < was.Cursor
		movedWhileDisabled := !was.Enabled && is.Cursor != was.Cursor
		if rewound || movedWhileDisabled || is.Enabled != was.Enabled {
			t.Errorf("%s restarted at cursor %d (enabled=%t), was at %d (enabled=%t)", name, is.Cursor, is.Enabled, was.Cursor, was.Enabled)
		}
	}
	if bells := consumer(t, after, "garden-seed-bells"); bells.Enabled || bells.Lag == 0 {
		t.Errorf("the killed consumer came back as %+v; want it still disabled, behind the writes made after the kill", bells)
	}
}

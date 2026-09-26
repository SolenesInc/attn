package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestBusStatusCountsADisabledConsumersLagFromTheHead(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	defineRequests(t, cli, gateNS)
	disabled := testworld.Request(app, protocol.BusSetConsumerEnabledMessage{
		Cmd: protocol.CmdBusSetConsumerEnabled, RequestID: "pause", Consumer: "garden-seed-bells", Enabled: false,
	}, protocol.EventBusSetConsumerEnabledResult, func(m protocol.BusSetConsumerEnabledResultMessage) bool { return m.RequestID == "pause" })
	if !disabled.Success {
		t.Fatalf("disabling garden-seed-bells: %s", protocol.Deref(disabled.Error))
	}
	before := busStatus(t, app)
	paused := consumer(t, before, "garden-seed-bells")

	first := put(t, cli, gateNS, "a", `{}`)
	for _, id := range []string{"b", "c"} {
		put(t, cli, gateNS, id, `{}`)
	}
	after := busStatus(t, app)
	behind := consumer(t, after, "garden-seed-bells")
	if after.Head < before.Head+3 {
		t.Errorf("three writes moved the head from %d to %d, want it at least 3 further", before.Head, after.Head)
	}
	heldShortOfTheWrites := behind.Cursor >= paused.Cursor && behind.Cursor < first.Seq
	if !heldShortOfTheWrites || behind.Lag != after.Head-behind.Cursor || behind.Enabled {
		t.Errorf("the disabled consumer reads %+v after writes from seq %d (was %+v); want its cursor held short of the writes, its lag counted to the head %d and still disabled", behind, first.Seq, paused, after.Head)
	}
}

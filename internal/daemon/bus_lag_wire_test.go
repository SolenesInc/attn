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

	for _, id := range []string{"a", "b", "c"} {
		put(t, cli, gateNS, id, `{}`)
	}
	after := busStatus(t, app)
	behind := consumer(t, after, "garden-seed-bells")
	if after.Head < before.Head+3 {
		t.Errorf("three writes moved the head from %d to %d, want it at least 3 further", before.Head, after.Head)
	}
	if behind.Cursor != paused.Cursor || behind.Lag != after.Head-behind.Cursor || behind.Lag <= paused.Lag || behind.Enabled {
		t.Errorf("the disabled consumer reads %+v after the writes (was %+v); want its cursor held, its lag grown to the head %d and still disabled", behind, paused, after.Head)
	}
}

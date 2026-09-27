package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func setBusConsumerEnabled(t *testing.T, app *testworld.Peer, consumer string, enabled bool) {
	t.Helper()
	requestID := consumer + time.Now().String()
	result := testworld.Request(app, protocol.BusSetConsumerEnabledMessage{
		Cmd: protocol.CmdBusSetConsumerEnabled, RequestID: requestID, Consumer: consumer, Enabled: enabled,
	}, protocol.EventBusSetConsumerEnabledResult, func(m protocol.BusSetConsumerEnabledResultMessage) bool { return m.RequestID == requestID })
	if !result.Success {
		t.Fatalf("set %s enabled=%t: %s", consumer, enabled, protocol.Deref(result.Error))
	}
}

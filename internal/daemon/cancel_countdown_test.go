package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestCancelCountdown_KeepsTheTurnOwed(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})

	if !turnIsOwed(d, id) {
		t.Fatal("the cancel settled the turn it was supposed to keep")
	}
}

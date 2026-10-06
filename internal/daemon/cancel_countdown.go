package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleCancelCountdown(msg *protocol.CancelCountdownMessage) {
	if d == nil || msg == nil {
		return
	}
	sessionID := protocol.TrimID(msg.SessionID)
	if sessionID == "" {
		return
	}

	settleAnswered := d.answerAutoSettleByUser(sessionID)

	if !settleAnswered {
		return
	}
	d.broadcastSessionStateChanged(sessionID)
}

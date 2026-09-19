package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleCancelCountdown(msg *protocol.CancelCountdownMessage) {
	if d == nil || msg == nil {
		return
	}
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}

	settleAnswered := d.answerAutoSettleByUser(sessionID)
	nudgeCancelled := d.cancelNudgeCountdownByUser(sessionID)

	if !settleAnswered && !nudgeCancelled {
		return
	}
	d.broadcastSessionStateChanged(sessionID)
}

package daemon

import "github.com/victorarias/attn/internal/protocol"

func (d *Daemon) handleCancelCountdown(msg *protocol.CancelCountdownMessage) error {
	if d == nil || msg == nil {
		return nil
	}
	session := protocol.TrimID(msg.SessionID)
	if session == "" {
		return nil
	}
	_, err := d.answerAutoSettleByUser(session)
	return err
}

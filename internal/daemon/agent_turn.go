package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleAgentTurn(conn net.Conn, msg *protocol.AgentTurnMessage) {
	outcome, err := d.agentTurn(protocol.TrimID(msg.SessionID), msg.Action)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AgentTurnResult: &protocol.AgentTurnResult{Outcome: outcome}})
}

func (d *Daemon) agentTurn(sessionID protocol.SessionID, action protocol.AgentTurnAction) (protocol.AgentTurnOutcome, error) {
	if !d.queueFlowOn() {
		return protocol.AgentTurnOutcomeQueueOff, nil
	}
	entry := d.monitorEntry(sessionID)
	if entry == nil {
		return "", fmt.Errorf("session %s is not running in attn", sessionID)
	}
	entry.mu.Lock()
	outcome, err := entry.agentTurnLocked(action, time.Now())
	changed := outcome == protocol.AgentTurnOutcomeOpened || outcome == protocol.AgentTurnOutcomeSettled
	var view monitorView
	if changed {
		view = entry.viewLocked()
	}
	entry.mu.Unlock()
	if changed {
		entry.sink.Applied(view)
	}
	return outcome, err
}

func (d *Daemon) inNotificationRun(sessionID protocol.SessionID) bool {
	entry := d.monitorEntry(sessionID)
	if entry == nil {
		return false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.input.inNotificationRunLocked()
}

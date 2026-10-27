package daemon

import (
	"encoding/json"
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
	result, err := d.monitorCommand(sessionID, "agent_turn", time.Time{}, action)
	return protocol.Deref(result.Outcome), err
}

func (d *Daemon) inNotificationRun(sessionID protocol.SessionID) bool {
	view, ok := d.monitorViewFor(sessionID)
	return ok && view.NotificationRun.V
}

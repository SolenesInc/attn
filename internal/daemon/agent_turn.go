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
	monitor := d.sessionMonitor(sessionID)
	if monitor == nil {
		return "", fmt.Errorf("session %s is not running in attn", sessionID)
	}
	return monitor.AgentTurn(action, time.Now())
}

func (d *Daemon) inNotificationRun(sessionID protocol.SessionID) bool {
	return d.sessionMonitor(sessionID).InNotificationRun()
}

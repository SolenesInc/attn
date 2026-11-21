package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) turnCommands() map[string]command.Handler {
	turn := func(action protocol.AgentTurnAction) command.Handler {
		return command.Typed(func(_ context.Context, c command.Caller, _ struct{}) (any, error) {
			outcome, err := d.agentTurn(c.Session, action)
			if err != nil {
				return nil, err
			}
			return protocol.AgentTurnResult{Outcome: outcome}, nil
		})
	}
	return map[string]command.Handler{
		"turn open":   turn(protocol.AgentTurnActionOpen),
		"turn settle": turn(protocol.AgentTurnActionSettle),
	}
}

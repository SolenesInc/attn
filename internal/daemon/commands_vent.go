package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) ventCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"vent": command.Typed(func(_ context.Context, c command.Caller, a command.VentArgs) (any, error) {
			return d.vent(c.Session, protocol.VentKind(a.About), a.Text)
		}),
		"vent withdraw": command.Typed(func(_ context.Context, c command.Caller, a command.VentWithdrawArgs) (any, error) {
			return d.withdrawVent(c.Session, a.Vent)
		}),
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) commandLayer() *command.Layer {
	d.commandsOnce.Do(func() {
		layer, err := command.New(d.seedCommands())
		if err != nil {
			panic(err)
		}
		d.commands = layer
	})
	return d.commands
}

func (d *Daemon) serveRunCommand(ctx context.Context, conn net.Conn, msg *protocol.RunCommandMessage) {
	args, err := json.Marshal(msg.Args)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	ctx, cancel := d.connectionContext(ctx, conn)
	defer cancel()
	reply, after := d.runCommand(ctx, d.callerFromFrame(ctx, msg), msg.Command, args)
	if err := json.NewEncoder(conn).Encode(reply); err != nil {
		d.logf("run_command %s: writing response: %v", msg.Command, err)
	}
	if after != nil {
		after()
	}
}

func (d *Daemon) callerFromFrame(ctx context.Context, msg *protocol.RunCommandMessage) command.Caller {
	profile := strings.TrimSpace(protocol.Deref(msg.ProfileID))
	if session := protocol.TrimID(protocol.Deref(msg.SessionID)); session != "" {
		return command.Caller{Session: session, Profile: profile}
	}
	terminal := protocol.TerminalID(strings.TrimSpace(string(protocol.Deref(msg.CallerID))))
	if terminal == "" {
		return command.Caller{Profile: profile}
	}
	if session := d.callerSession(ctx, terminal); session != "" {
		return command.Caller{Session: session, Profile: profile}
	}
	if threadProfile, _, ok := harness.ParseCodexThreadTerminal(terminal); ok && profile == "" {
		profile = threadProfile
	}
	return command.Caller{Profile: profile, Terminal: terminal}
}

func (d *Daemon) runCommand(ctx context.Context, c command.Caller, name string, args json.RawMessage) (protocol.Response, func()) {
	result, refusal := d.commandLayer().Run(ctx, c, name, args)
	if then, ok := result.(command.Then); ok {
		return commandResponse(then.Result, nil), then.After
	}
	return commandResponse(result, refusal), nil
}

func commandResponse(result any, refusal *command.Refusal) protocol.Response {
	if refusal == nil {
		return protocol.Response{Ok: true, CommandResult: result}
	}
	reply := protocol.Response{Ok: false, CommandRefusal: &protocol.CommandRefusal{Code: refusal.Code, Message: refusal.Message}}
	if refusal.Fix != "" {
		reply.CommandRefusal.Fix = protocol.Ptr(refusal.Fix)
	}
	if !refusal.Arguments {
		reply.Error = protocol.Ptr(refusal.Message)
		reply.Refusal = protocol.Ptr(protocol.Refusal(refusal.Code))
	}
	return reply
}

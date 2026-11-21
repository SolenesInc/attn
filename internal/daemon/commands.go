package daemon

import (
	"context"
	"encoding/json"
	"maps"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/reading"
)

func (d *Daemon) commandLayer() *command.Layer {
	d.commandsOnce.Do(func() {
		handlers := d.seedCommands()
		for _, family := range []map[string]command.Handler{
			{"state explain": command.Typed(d.runStateExplain)},
			{"list": command.Typed(d.runList)},
			{"activity": command.Typed(d.runActivity), "activity clear": command.Typed(d.runActivityClear)},
			d.calloutCommands(),
			d.delegateCommands(),
			d.delegateRolesCommands(),
			d.reviewCommands(),
			d.openCommands(),
			d.navCommands(),
			d.sessionCommands(),
			reading.Handlers(),
			d.agentCommands(),
			d.turnCommands(),
			d.ventCommands(),
			d.onboardingCommands(),
			d.presenceCommands(),
			d.handoffCommands(),
			d.askCommands(),
			d.browserCommands(),
			d.terminalCommands(),
		} {
			maps.Copy(handlers, family)
		}
		layer, err := command.New(handlers)
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
	caller, err := d.callerFromFrame(ctx, msg)
	if err != nil {
		if err := json.NewEncoder(conn).Encode(command.Response(nil, command.AsRefusal(err))); err != nil {
			d.logf("run_command %s: writing response: %v", msg.Command, err)
		}
		return
	}
	reply, after := d.runCommand(ctx, caller, msg.Command, args)
	if err := json.NewEncoder(conn).Encode(reply); err != nil {
		d.logf("run_command %s: writing response: %v", msg.Command, err)
	}
	if after != nil {
		after()
	}
}

func (d *Daemon) callerFromFrame(ctx context.Context, msg *protocol.RunCommandMessage) (command.Caller, error) {
	profile := strings.TrimSpace(protocol.Deref(msg.ProfileID))
	terminal := protocol.TerminalID(strings.TrimSpace(string(protocol.Deref(msg.CallerID))))
	if session := protocol.TrimID(protocol.Deref(msg.SessionID)); session != "" {
		if terminal != "" && d.inAnotherProfile(d.callerSession(ctx, terminal), session) {
			return command.Caller{}, protocol.Refuse(protocol.RefusalNotFound, "unknown session %s", session)
		}
		return d.framedSessionCaller(session, profile)
	}
	if terminal == "" {
		return command.Caller{Profile: profile}, nil
	}
	if session := d.callerSession(ctx, terminal); session != "" {
		return d.framedSessionCaller(session, profile)
	}
	if threadProfile, _, ok := harness.ParseCodexThreadTerminal(terminal); ok && profile == "" {
		profile = threadProfile
	}
	return command.Caller{Profile: profile, Terminal: terminal}, nil
}

func (d *Daemon) framedSessionCaller(session protocol.SessionID, requested string) (command.Caller, error) {
	c := d.sessionCaller(session)
	if c.Profile == "" {
		c.Profile = requested
		return c, nil
	}
	if requested != "" {
		if _, err := d.resolveGardenProfile(session, requested, ""); err != nil {
			return command.Caller{}, err
		}
	}
	return c, nil
}

func (d *Daemon) inAnotherProfile(asking, named protocol.SessionID) bool {
	if asking == "" || asking == named {
		return false
	}
	askingProfile, askingErr := d.store.SessionProfileID(asking)
	namedProfile, namedErr := d.store.SessionProfileID(named)
	return askingErr == nil && namedErr == nil && askingProfile != namedProfile
}

func (d *Daemon) runCommand(ctx context.Context, c command.Caller, name string, args json.RawMessage) (protocol.Response, func()) {
	result, refusal := d.commandLayer().Run(ctx, c, name, args)
	if then, ok := result.(command.Then); ok {
		return command.Response(then.Result, nil), then.After
	}
	return command.Response(result, refusal), nil
}

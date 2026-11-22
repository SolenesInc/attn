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
			d.journalCommands(),
			d.crewCommands(),
			d.prCommands(),
			d.automationCommands(),
			d.settingsCommands(),
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
	ctx, cancel := d.connectionContext(ctx, conn)
	defer cancel()
	caller, err := d.callerFromFrame(ctx, msg)
	if err != nil {
		if err := json.NewEncoder(conn).Encode(command.Response(nil, command.AsRefusal(err))); err != nil {
			d.logf("run_command %s: writing response: %v", msg.Command, err)
		}
		return
	}
	reply, after := d.runCommand(ctx, caller, msg.Command, msg.Args)
	if err := json.NewEncoder(conn).Encode(reply); err != nil {
		d.logf("run_command %s: writing response: %v", msg.Command, err)
	}
	if after != nil {
		after()
	}
}

func (d *Daemon) callerFromFrame(ctx context.Context, msg *protocol.RunCommandMessage) (command.Caller, error) {
	c, err := d.framedCaller(ctx, msg)
	c.Directory = strings.TrimSpace(protocol.Deref(msg.Directory))
	c.GitEnvironment = msg.GitEnvironment
	return c, err
}

func (d *Daemon) framedCaller(ctx context.Context, msg *protocol.RunCommandMessage) (command.Caller, error) {
	requested := strings.TrimSpace(protocol.Deref(msg.ProfileID))
	terminal := protocol.TerminalID(strings.TrimSpace(string(protocol.Deref(msg.CallerID))))
	role := command.CallerRole(msg.Command)
	var asking command.Caller
	if terminal != "" {
		if session := d.callerSession(ctx, terminal); session != "" {
			profile, known := d.knownSessionProfile(session)
			if !known {
				return command.Caller{}, unknownFramedSession(role, session)
			}
			asking = command.Caller{Session: session, Profile: profile}
		} else if _, remote := OriginOf(ctx); !remote {
			asking.Profile, _, _ = harness.ParseCodexThreadTerminal(terminal)
		}
	}
	if named := protocol.TrimID(protocol.Deref(msg.SessionID)); named != "" {
		session, profile, err := d.resolveFramedSession(msg.Command, named, asking.Profile)
		if err != nil {
			return command.Caller{}, err
		}
		return d.sessionCallerFor(session, profile, requested)
	}
	if asking.Session != "" {
		return d.sessionCallerFor(asking.Session, asking.Profile, requested)
	}
	profile, err := d.requestedProfileID(requested)
	if err != nil {
		return command.Caller{}, err
	}
	if threadProfile, _, ok := harness.ParseCodexThreadTerminal(terminal); ok && profile == "" {
		profile = threadProfile
	}
	return command.Caller{Profile: profile, Terminal: terminal}, nil
}

func (d *Daemon) sessionCallerFor(session protocol.SessionID, profile, requested string) (command.Caller, error) {
	if requested != "" {
		if _, err := d.resolveGardenProfile(session, requested, ""); err != nil {
			return command.Caller{}, err
		}
	}
	return command.Caller{Session: session, Profile: profile}, nil
}

func (d *Daemon) resolveFramedSession(cmd string, named protocol.SessionID, scope string) (protocol.SessionID, string, error) {
	if profile, known := d.knownSessionProfile(named); known && (scope == "" || profile == scope) {
		return named, profile, nil
	}
	session, code := d.resolveSessionReference(string(named), scope)
	target := command.SessionFlagNamesTarget(cmd)
	switch {
	case session != nil:
		return session.ID, session.ProfileID, nil
	case code == "ambiguous_session" && target:
		return "", "", command.Refuse("ambiguous_session", "session %s matches more than one session; give more of the id", named)
	case code == "ambiguous_session":
		return "", "", command.Refuse("ambiguous_session", "the %s %q matches more than one session; give more of the id", command.CallerRole(cmd), named)
	case target:
		return "", "", command.Refuse(command.CodeSessionNotFound, "unknown session %s", named)
	default:
		return "", "", unknownFramedSession(command.CallerRole(cmd), named)
	}
}

func (d *Daemon) knownSessionProfile(id protocol.SessionID) (string, bool) {
	if session := d.store.Get(id); session != nil {
		return session.ProfileID, true
	}
	if entry := d.store.SessionLedgerEntry(id); entry != nil {
		return entry.ProfileID, true
	}
	return "", false
}

func unknownFramedSession(role string, id protocol.SessionID) error {
	return command.Refuse(command.CodeSessionNotFound, "the %s %q is not a session on this daemon", role, id)
}

func (d *Daemon) requestedProfileID(requested string) (string, error) {
	if requested == "" || d.enrollmentStatus().IsOutpost() {
		return requested, nil
	}
	profile, err := d.resolveGardenProfile("", requested, "")
	if err != nil {
		return "", err
	}
	return profile.ID, nil
}

func (d *Daemon) runCommand(ctx context.Context, c command.Caller, name string, args json.RawMessage) (protocol.Response, func()) {
	result, refusal := d.commandLayer().Run(ctx, c, name, args)
	if then, ok := result.(command.Then); ok {
		return command.Response(then.Result, nil), then.After
	}
	return command.Response(result, refusal), nil
}

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/command"
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
			reading.Handlers(commandRules),
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
			d.catalogCommands,
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

func (d *Daemon) serveRunCommand(ctx context.Context, conn net.Conn, msg *protocol.RunCommandMessage, framing command.Caller) {
	ctx, cancel := d.connectionContext(ctx, conn)
	defer cancel()
	caller, err := d.callerFromFrame(msg, framing)
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

func (d *Daemon) callerFromFrame(msg *protocol.RunCommandMessage, framing command.Caller) (command.Caller, error) {
	c, err := d.framedCaller(msg, framing)
	c.Directory = strings.TrimSpace(protocol.Deref(msg.Directory))
	c.GitEnvironment = msg.GitEnvironment
	return c, err
}

func (d *Daemon) framedCaller(msg *protocol.RunCommandMessage, framing command.Caller) (command.Caller, error) {
	requested := strings.TrimSpace(protocol.Deref(msg.ProfileID))
	terminal := protocol.TerminalID(strings.TrimSpace(string(protocol.Deref(msg.CallerID))))
	role := command.CallerRole(msg.Command)
	var asking command.Caller
	if terminal != "" {
		asking = framing
		if asking.Session != "" {
			profile, known := d.knownSessionProfile(asking.Session)
			if !known {
				return command.Caller{}, unknownFramedSession(role, asking.Session)
			}
			asking.Profile = profile
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
	if profile == "" {
		profile = asking.Profile
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
	target := command.SessionFlagNamesTarget(cmd)
	resolve := d.resolveSession
	if target {
		resolve = d.resolveTargetSession
	}
	match, err := resolve(string(named), scope)
	switch {
	case err == nil:
		return match.ID(), match.ProfileID(), nil
	case errors.Is(err, errAmbiguousSession) && target:
		return "", "", command.Refuse(command.CodeAmbiguousSession, "%s", ambiguousSessionMessage(string(named), err, "session %s matches more than one session; give more of the full id"))
	case errors.Is(err, errAmbiguousSession):
		return "", "", command.Refuse(command.CodeAmbiguousSession, "the %s %q matches more than one session; give more of the full id", command.CallerRole(cmd), named)
	case !errors.Is(err, errSessionNotFound):
		return "", "", err
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

package daemon

import (
	"context"
	"strings"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) seedCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"seed show":  command.Typed(d.runSeedShow),
		"seed notes": command.Typed(d.runSeedNotes),
		"seed note":  command.Typed(d.runSeedNote),
		"seed ready": command.Typed(d.runSeedReady),
		"seed prime": command.Typed(d.runSeedPrime),
	}
}

func gardenScope(c command.Caller) (*protocol.SessionID, *string) {
	var session *protocol.SessionID
	if c.Session != "" {
		session = protocol.Ptr(c.Session)
	}
	var profile *string
	if c.Profile != "" {
		profile = protocol.Ptr(c.Profile)
	}
	return session, profile
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func bookmark(since *int64) (*int, error) {
	if since == nil {
		return nil, nil
	}
	if *since < 0 {
		return nil, command.Usage("--since %d is not a bookmark; pass the number a read printed on its bookmark line", *since)
	}
	return protocol.Ptr(int(*since)), nil
}

func (d *Daemon) runSeedShow(ctx context.Context, c command.Caller, args command.SeedShowArgs) (any, error) {
	if args.Since != nil && args.Section != "" {
		return nil, command.Usage("--since reads log entries and --section reads the body; pass one")
	}
	since, err := bookmark(args.Since)
	if err != nil {
		return nil, err
	}
	msg := protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: args.Seed, Since: since}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scopeGardenRequest(msg.Cmd, &msg, ""); err != nil {
		return nil, err
	}
	return d.seedShow(ctx, &msg)
}

func (d *Daemon) runSeedNotes(_ context.Context, c command.Caller, args command.SeedNotesArgs) (any, error) {
	since, err := bookmark(args.Since)
	if err != nil {
		return nil, err
	}
	msg := protocol.SeedNotesMessage{Cmd: protocol.CmdSeedNotes, SeedID: args.Seed, Since: since, Kind: optionalText(strings.TrimSpace(args.Kind))}
	if args.Limit != nil && *args.Limit > 0 {
		msg.Limit = protocol.Ptr(int(*args.Limit))
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scopeGardenRequest(msg.Cmd, &msg, ""); err != nil {
		return nil, err
	}
	return d.seedNotesRead(&msg)
}

func (d *Daemon) runSeedNote(_ context.Context, c command.Caller, args command.SeedNoteArgs) (any, error) {
	msg := protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: args.Seed, Body: args.Text}
	if args.Handoff {
		msg.Kind = protocol.Ptr("handoff")
	}
	if args.Ring {
		msg.Ring = protocol.Ptr(true)
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scopeGardenRequest(msg.Cmd, &msg, ""); err != nil {
		return nil, err
	}
	return d.seedNote(&msg, d.sessionOrOwner(c.Session))
}

func (d *Daemon) runSeedReady(_ context.Context, c command.Caller, args command.SeedReadyArgs) (any, error) {
	msg := protocol.SeedReadyMessage{Cmd: protocol.CmdSeedReady, Plot: optionalText(strings.TrimSpace(args.Plot))}
	if args.All {
		msg.All = protocol.Ptr(true)
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scopeGardenRequest(msg.Cmd, &msg, ""); err != nil {
		return nil, err
	}
	return d.seedReady(&msg)
}

func (d *Daemon) runSeedPrime(ctx context.Context, c command.Caller, _ command.SeedPrimeArgs) (any, error) {
	return d.runSeedReady(ctx, c, command.SeedReadyArgs{})
}

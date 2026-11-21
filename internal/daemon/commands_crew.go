package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) crewCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"crew list":      command.Typed(d.runCrewList),
		"crew create":    command.Typed(d.runCrewCreate),
		"crew start":     command.Typed(d.runCrewStart),
		"crew close":     command.Typed(d.runCrewClose),
		"crew prime":     command.Typed(d.runCrewPrime),
		"crew apply":     command.Typed(d.runCrewApply),
		"crew set":       command.Typed(d.runCrewSet),
		"crew rename":    command.Typed(d.runCrewRename),
		"crew move-home": command.Typed(d.runCrewMoveHome),
		"crew retire":    command.Typed(d.runCrewRetire),
		"crew restore":   command.Typed(d.runCrewRestore),
	}
}

func crewAskerOf(c command.Caller) crewAsker {
	return crewAsker{session: c.Session, profileID: c.Profile}
}

func crewFailed(verb string, err error) error {
	if refusal := (*command.Refusal)(nil); errors.As(err, &refusal) {
		return err
	}
	return fmt.Errorf("crew %s: %w", verb, err)
}

func (c crewAsker) source() (*protocol.SessionID, *string) {
	var session *protocol.SessionID
	if c.session != "" {
		session = protocol.Ptr(c.session)
	}
	var profile *string
	if c.profileID != "" {
		profile = protocol.Ptr(c.profileID)
	}
	return session, profile
}

func (d *Daemon) runCrewList(_ context.Context, c command.Caller, a command.CrewListArgs) (any, error) {
	profileID, err := d.crewAskerProfile(crewAskerOf(c))
	if err != nil {
		return nil, crewFailed("list", err)
	}
	members, docs, err := d.readCrewMembers()
	if err != nil {
		return nil, crewFailed("list", err)
	}
	out := make([]protocol.CrewMember, 0, len(members))
	for _, member := range members {
		if member.ProfileID != profileID || (member.Retired && !a.All) {
			continue
		}
		out = append(out, d.crewMemberWire(member, docs[member.ID].Rev))
	}
	return protocol.CrewListResult{Members: out}, nil
}

func (d *Daemon) runCrewCreate(_ context.Context, c command.Caller, a command.CrewCreateArgs) (any, error) {
	profileID, err := d.crewAskerProfile(crewAskerOf(c))
	if err != nil {
		return nil, crewFailed("create", err)
	}
	created, err := d.createCrewMember(profileID, a.Name)
	if err != nil {
		return nil, crewFailed("create", err)
	}
	member, doc, err := d.crewMember(created.ID)
	if err != nil {
		return nil, crewFailed("create", err)
	}
	return d.crewMemberWire(member, doc.Rev), nil
}

func (d *Daemon) runCrewStart(_ context.Context, c command.Caller, a command.CrewStartArgs) (any, error) {
	msg := &protocol.CrewStartMessage{Member: a.Member, Agent: optionalText(strings.TrimSpace(a.Agent))}
	msg.SourceSessionID, msg.ProfileID = crewAskerOf(c).source()
	result, err := d.crewStartAsked(msg, crewStartRequest{RequestedBy: d.launchRequester(c.Session, "attn crew start")})
	if err != nil {
		return nil, crewFailed("start", err)
	}
	return result, nil
}

func (d *Daemon) runCrewClose(_ context.Context, c command.Caller, a command.CrewCloseArgs) (any, error) {
	result, err := d.crewClose(crewAskerOf(c), a.Member)
	if err != nil {
		return nil, crewFailed("close", err)
	}
	return result, nil
}

func (d *Daemon) runCrewPrime(_ context.Context, c command.Caller, _ command.CrewPrimeArgs) (any, error) {
	result := &protocol.CrewPrimeResult{AwarenessDirs: []string{}}
	member, block, bound, err := d.crewPrimeForSession(c.Session)
	if err != nil {
		return nil, crewFailed("prime", err)
	}
	if bound {
		result.Member = protocol.Ptr(member.ID)
		result.Guidance = protocol.Ptr(block)
		result.AwarenessDirs = append(result.AwarenessDirs, member.AwarenessDirs...)
		result.PrimingBytes = len(block)
		result.V2Enabled = protocol.Ptr(true)
	}
	return result, nil
}

func (d *Daemon) runCrewApply(_ context.Context, c command.Caller, a command.CrewApplyArgs) (any, error) {
	result, err := d.crewApply(crewAskerOf(c), a.Member)
	if err != nil {
		return nil, crewFailed("apply", err)
	}
	return result, nil
}

func (d *Daemon) runCrewSet(_ context.Context, c command.Caller, a command.CrewSetArgs) (any, error) {
	msg := &protocol.CrewSetMessage{
		Member:            a.Member,
		Cwd:               a.Cwd,
		Agent:             a.Agent,
		Model:             a.Model,
		Effort:            a.Effort,
		LaunchDesktop:     optionalText(a.LaunchDesktop),
		LaunchDesktopName: optionalText(a.DesktopName),
		Home:              optionalText(a.Home),
	}
	msg.SourceSessionID, msg.ProfileID = crewAskerOf(c).source()
	if msg.LaunchDesktopName != nil && msg.LaunchDesktop == nil {
		return nil, command.Refuse(command.CodeDesktopNameNeedsDesktop, "{desktop_name} needs {launch_desktop} own or an empty slot")
	}
	if a.AwarenessDirs != nil {
		var dirs []string
		for _, dir := range a.AwarenessDirs {
			if strings.TrimSpace(dir) != "" {
				dirs = append(dirs, dir)
			}
		}
		if len(dirs) == 0 {
			msg.ClearAwarenessDirs = protocol.Ptr(true)
		} else {
			msg.AwarenessDirs = dirs
		}
	}
	if msg.Cwd == nil && msg.Agent == nil && msg.Model == nil && msg.Effort == nil && msg.LaunchDesktop == nil && msg.Home == nil && a.AwarenessDirs == nil {
		return nil, command.Refuse(command.CodeNothingToSet, "nothing to set — pass {cwd}, {agent}, {model}, {effort}, {launch_desktop}, {awareness_dirs}, {home}, or any of them together")
	}
	member, conflict, err := d.crewSet(msg)
	if err != nil {
		return nil, crewFailed("set", err)
	}
	if conflict {
		return nil, crewFailed("set", errors.New("the member changed after it was read; reconcile the returned revision and retry"))
	}
	return member, nil
}

func (d *Daemon) runCrewRename(_ context.Context, c command.Caller, a command.CrewRenameArgs) (any, error) {
	msg := &protocol.CrewRenameMessage{Member: a.Member, Name: a.Name}
	msg.SourceSessionID, msg.ProfileID = crewAskerOf(c).source()
	result, err := d.crewRename(msg)
	if err != nil {
		return nil, crewFailed("rename", err)
	}
	return result, nil
}

func (d *Daemon) runCrewMoveHome(_ context.Context, c command.Caller, a command.CrewMoveHomeArgs) (any, error) {
	result, err := d.crewMoveHome(crewAskerOf(c), a.Member)
	if err != nil {
		return nil, crewFailed("move-home", err)
	}
	return result, nil
}

func (d *Daemon) runCrewRetire(_ context.Context, c command.Caller, a command.CrewRetireArgs) (any, error) {
	member, err := d.crewAskedMember(crewAskerOf(c), a.Member)
	if err != nil {
		return nil, crewFailed("retire", err)
	}
	result, err := d.retireCrewMember(member)
	if err != nil {
		return nil, crewFailed("retire", err)
	}
	return result, nil
}

func (d *Daemon) runCrewRestore(_ context.Context, c command.Caller, a command.CrewRestoreArgs) (any, error) {
	member, err := d.crewAskedMember(crewAskerOf(c), a.Member)
	if err != nil {
		return nil, crewFailed("restore", err)
	}
	d.crewStartMu.Lock()
	restored, err := d.store.RestoreCrewMember(member.ID)
	d.crewStartMu.Unlock()
	if err != nil {
		return nil, crewFailed("restore", err)
	}
	d.publishFact(FactCrewUpdated, member.ID, nil)
	d.kickInbox(who.Member(member.Key).Address())
	member, doc, err := d.crewMember(member.ID)
	if err != nil {
		return nil, crewFailed("restore", err)
	}
	return protocol.CrewRestoreResult{Member: d.crewMemberWire(member, doc.Rev), AlreadyActive: !restored}, nil
}

package daemon

import (
	"context"
	"sort"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) agentCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"agent list":       command.Typed(d.runAgentList),
		"agent peek":       command.Typed(d.runAgentPeek),
		"agent msg":        command.Typed(d.runAgentMsg),
		"agent close":      command.Typed(d.runAgentClose),
		"agent reload":     command.Typed(d.runAgentReload),
		"agent inbox":      command.Typed(d.runAgentInbox),
		"agent msg-status": command.Typed(d.runAgentMsgStatus),
	}
}

func (d *Daemon) runAgentList(ctx context.Context, c command.Caller, a command.AgentListArgs) (any, error) {
	profiles, err := d.liveProtocolProfiles()
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(profiles))
	for _, profile := range profiles {
		names[profile.ID] = profile.Name
	}
	rows := []protocol.AgentListRow{}
	for _, session := range d.sessionsForBroadcast(d.store.List("")) {
		if !c.Sees(session.ProfileID) {
			continue
		}
		row := protocol.AgentListRow{
			ID: string(session.ID), Label: session.Label, Agent: string(session.Agent), Profile: names[session.ProfileID],
			Directory: session.Directory, State: string(session.State), TurnOwed: protocol.Deref(session.TurnOwed),
			Member: protocol.Deref(session.CrewMemberName),
		}
		if key := protocol.Deref(session.CrewMember); key != "" {
			row.MemberKey = protocol.Ptr(key)
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Profile != rows[j].Profile {
			return rows[i].Profile < rows[j].Profile
		}
		return rows[i].Label < rows[j].Label
	})
	return protocol.AgentListResult{Agents: rows}, nil
}

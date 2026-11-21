package client

import (
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) CrewList() (*protocol.CrewListResult, error) {
	return commandResult[protocol.CrewListResult](c, "crew list", "", command.CrewListArgs{})
}

func (c *Client) CrewListAll() (*protocol.CrewListResult, error) {
	return commandResult[protocol.CrewListResult](c, "crew list", "", command.CrewListArgs{All: true})
}

func (c *Client) CrewStart(member string, agent string, sourceSessionID protocol.SessionID) (*protocol.CrewStartResult, error) {
	return commandResult[protocol.CrewStartResult](c, "crew start", sourceSessionID, command.CrewStartArgs{Member: member, Agent: agent})
}

func (c *Client) CrewClose(member string) (*protocol.CrewCloseResult, error) {
	return commandResult[protocol.CrewCloseResult](c, "crew close", "", command.CrewCloseArgs{Member: member})
}

func (c *Client) CrewApply(member string) (*protocol.CrewApplyResult, error) {
	return commandResult[protocol.CrewApplyResult](c, "crew apply", "", command.CrewApplyArgs{Member: member})
}

func (c *Client) CrewSet(member string, cwd, agent, model, effort *string, awarenessDirs []string) (*protocol.CrewMember, error) {
	return c.CrewSetMessage(protocol.CrewSetMessage{Member: member, Cwd: cwd, Agent: agent, Model: model, Effort: effort, AwarenessDirs: awarenessDirs})
}

func (c *Client) CrewSetMessage(msg protocol.CrewSetMessage) (*protocol.CrewMember, error) {
	args := command.CrewSetArgs{
		Member:        msg.Member,
		Cwd:           msg.Cwd,
		Agent:         msg.Agent,
		Model:         msg.Model,
		Effort:        msg.Effort,
		LaunchDesktop: protocol.Deref(msg.LaunchDesktop),
		DesktopName:   protocol.Deref(msg.LaunchDesktopName),
		Home:          protocol.Deref(msg.Home),
		AwarenessDirs: msg.AwarenessDirs,
	}
	if protocol.Deref(msg.ClearAwarenessDirs) || (msg.AwarenessDirs != nil && len(msg.AwarenessDirs) == 0) {
		args.AwarenessDirs = []string{""}
	}
	return commandResult[protocol.CrewMember](c, "crew set", "", args)
}

func (c *Client) CrewPrime(sessionID protocol.SessionID) (*protocol.CrewPrimeResult, error) {
	return commandResult[protocol.CrewPrimeResult](c, "crew prime", sessionID, command.CrewPrimeArgs{})
}

func (c *Client) CrewHandoff(sessionID protocol.SessionID, note string, retry bool) (*protocol.CrewHandoffResult, error) {
	var result protocol.CrewHandoffResult
	if err := c.RunCommand("handoff", sessionID, map[string]any{"letter": note, "retry": retry}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) CrewMoveHome(member string) (*protocol.CrewMoveHomeResult, error) {
	return commandResult[protocol.CrewMoveHomeResult](c, "crew move-home", "", command.CrewMoveHomeArgs{Member: member})
}

func (c *Client) CrewRename(member, name string) (*protocol.CrewRenameResult, error) {
	return commandResult[protocol.CrewRenameResult](c, "crew rename", "", command.CrewRenameArgs{Member: member, Name: name})
}

func (c *Client) CrewCreate(name string) (*protocol.CrewMember, error) {
	return commandResult[protocol.CrewMember](c, "crew create", "", command.CrewCreateArgs{Name: name})
}

func (c *Client) CrewRetire(member string) (*protocol.CrewRetireResult, error) {
	return commandResult[protocol.CrewRetireResult](c, "crew retire", "", command.CrewRetireArgs{Member: member})
}

func (c *Client) CrewRestore(member string) (*protocol.CrewRestoreResult, error) {
	return commandResult[protocol.CrewRestoreResult](c, "crew restore", "", command.CrewRestoreArgs{Member: member})
}

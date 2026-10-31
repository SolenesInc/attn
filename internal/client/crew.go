package client

import (
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) CrewList() (*protocol.CrewListResult, error) {
	return c.crewList(protocol.CrewListMessage{Cmd: protocol.CmdCrewList})
}

func (c *Client) CrewListAll() (*protocol.CrewListResult, error) {
	return c.crewList(protocol.CrewListMessage{Cmd: protocol.CmdCrewList, IncludeRetired: protocol.Ptr(true)})
}

func (c *Client) crewList(msg protocol.CrewListMessage) (*protocol.CrewListResult, error) {
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.CrewListResult == nil {
		return nil, fmt.Errorf("the daemon answered without a roster")
	}
	return resp.CrewListResult, nil
}

func (c *Client) CrewWake(member string, agent string, sourceSessionID protocol.SessionID) (*protocol.CrewWakeResult, error) {
	msg := protocol.CrewWakeMessage{Cmd: protocol.CmdCrewWake, Member: member}
	if agent != "" {
		msg.Agent = protocol.Ptr(agent)
	}
	if sourceSessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sourceSessionID)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.CrewWakeResult == nil {
		return nil, fmt.Errorf("the daemon answered without a wake result")
	}
	return resp.CrewWakeResult, nil
}

func (c *Client) CrewSleep(member string) (*protocol.CrewSleepResult, error) {
	resp, err := c.send(protocol.CrewSleepMessage{Cmd: protocol.CmdCrewSleep, Member: member})
	if err != nil {
		return nil, err
	}
	if resp.CrewSleepResult == nil {
		return nil, fmt.Errorf("the daemon answered without a sleep result")
	}
	return resp.CrewSleepResult, nil
}

func (c *Client) CrewApply(member string) (*protocol.CrewApplyResult, error) {
	resp, err := c.send(protocol.CrewApplyMessage{Cmd: protocol.CmdCrewApply, Member: member})
	if err != nil {
		return nil, err
	}
	if resp.CrewApplyResult == nil {
		return nil, fmt.Errorf("the daemon answered without saying how the settings were applied")
	}
	return resp.CrewApplyResult, nil
}

func (c *Client) CrewSet(member string, cwd, agent, model, effort *string, awarenessDirs []string) (*protocol.CrewSetResult, error) {
	return c.CrewSetMessage(protocol.CrewSetMessage{Member: member, Cwd: cwd, Agent: agent, Model: model, Effort: effort, AwarenessDirs: awarenessDirs})
}

func (c *Client) CrewSetMessage(msg protocol.CrewSetMessage) (*protocol.CrewSetResult, error) {
	msg.Cmd = protocol.CmdCrewSet
	if msg.AwarenessDirs != nil && len(msg.AwarenessDirs) == 0 {
		msg.ClearAwarenessDirs = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.CrewSetResult == nil {
		return nil, fmt.Errorf("the daemon answered without a member")
	}
	return resp.CrewSetResult, nil
}

func (c *Client) CrewPrime(sessionID protocol.SessionID) (*protocol.CrewPrimeResult, error) {
	resp, err := c.send(protocol.CrewPrimeMessage{Cmd: protocol.CmdCrewPrime, SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	if resp.CrewPrimeResult == nil {
		return nil, fmt.Errorf("the daemon answered without a priming")
	}
	return resp.CrewPrimeResult, nil
}

func (c *Client) CrewHandoff(sessionID protocol.SessionID, note string, retry bool) (*protocol.CrewHandoffResult, error) {
	msg := protocol.CrewHandoffMessage{Cmd: protocol.CmdCrewHandoff, SessionID: sessionID, Note: note}
	if retry {
		msg.Retry = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.CrewHandoffResult == nil {
		return nil, fmt.Errorf("the daemon answered without saying where the letter landed")
	}
	return resp.CrewHandoffResult, nil
}

func (c *Client) CrewMoveHome(member string) (*protocol.CrewMoveHomeResult, error) {
	resp, err := c.send(protocol.CrewMoveHomeMessage{Cmd: protocol.CmdCrewMoveHome, Member: member})
	if err != nil {
		return nil, err
	}
	if resp.CrewMoveHomeResult == nil {
		return nil, fmt.Errorf("the daemon answered without a move-home result")
	}
	return resp.CrewMoveHomeResult, nil
}

func (c *Client) CrewRename(member, name string) (*protocol.CrewRenameResult, error) {
	resp, err := c.send(protocol.CrewRenameMessage{Cmd: protocol.CmdCrewRename, Member: member, Name: name})
	if err != nil {
		return nil, err
	}
	if resp.CrewRenameResult == nil {
		return nil, fmt.Errorf("the daemon answered without a rename result")
	}
	return resp.CrewRenameResult, nil
}

func (c *Client) CrewCreate(name string) (*protocol.CrewCreateResult, error) {
	resp, err := c.send(protocol.CrewCreateMessage{Cmd: protocol.CmdCrewCreate, Name: name})
	if err != nil {
		return nil, err
	}
	if resp.CrewCreateResult == nil {
		return nil, fmt.Errorf("the daemon answered without the new member")
	}
	return resp.CrewCreateResult, nil
}

func (c *Client) CrewRetire(member string) (*protocol.CrewRetireResult, error) {
	resp, err := c.send(protocol.CrewRetireMessage{Cmd: protocol.CmdCrewRetire, Member: member})
	if err != nil {
		return nil, err
	}
	if resp.CrewRetireResult == nil {
		return nil, fmt.Errorf("the daemon answered without a retirement result")
	}
	return resp.CrewRetireResult, nil
}

func (c *Client) CrewRestore(member string) (*protocol.CrewRestoreResult, error) {
	resp, err := c.send(protocol.CrewRestoreMessage{Cmd: protocol.CmdCrewRestore, Member: member})
	if err != nil {
		return nil, err
	}
	if resp.CrewRestoreResult == nil {
		return nil, fmt.Errorf("the daemon answered without a restore result")
	}
	return resp.CrewRestoreResult, nil
}

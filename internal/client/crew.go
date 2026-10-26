package client

import (
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) CrewList() (*protocol.CrewListResult, error) {
	resp, err := c.send(protocol.CrewListMessage{Cmd: protocol.CmdCrewList})
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

func (c *Client) CrewSet(member string, cwd, agent, model, effort *string, awarenessDirs []string) (*protocol.CrewSetResult, error) {
	return c.CrewSetWithNamedDesktop(member, cwd, agent, model, effort, awarenessDirs, nil, nil)
}
func (c *Client) CrewSetWithNamedDesktop(member string, cwd, agent, model, effort *string, awarenessDirs []string, desktop, name *string) (*protocol.CrewSetResult, error) {
	msg := protocol.CrewSetMessage{
		LaunchDesktop: desktop, LaunchDesktopName: name,
		Cmd: protocol.CmdCrewSet, Member: member, Cwd: cwd, Agent: agent, Model: model, Effort: effort, AwarenessDirs: awarenessDirs,
	}
	if awarenessDirs != nil && len(awarenessDirs) == 0 {
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

func (c *Client) CrewHandoff(sessionID protocol.SessionID, note string, retry bool, close protocol.CrewDayClose, day string) (*protocol.CrewHandoffResult, error) {
	msg := protocol.CrewHandoffMessage{Cmd: protocol.CmdCrewHandoff, SessionID: sessionID, Note: note, Day: protocol.Ptr(day)}
	if retry {
		msg.Retry = protocol.Ptr(true)
	}
	if close != "" {
		msg.Close = protocol.Ptr(close)
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

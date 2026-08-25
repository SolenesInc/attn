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

func (c *Client) CrewWake(member, agent string) (*protocol.CrewWakeResult, error) {
	msg := protocol.CrewWakeMessage{Cmd: protocol.CmdCrewWake, Member: member}
	if agent != "" {
		msg.Agent = protocol.Ptr(agent)
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

// awarenessDirs non-nil and empty clears the list, and travels as its own flag because an empty list marshals away.
func (c *Client) CrewSet(member string, cwd, agent, model *string, awarenessDirs []string) (*protocol.CrewSetResult, error) {
	msg := protocol.CrewSetMessage{
		Cmd: protocol.CmdCrewSet, Member: member, Cwd: cwd, Agent: agent, Model: model, AwarenessDirs: awarenessDirs,
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

func (c *Client) CrewPrime(sessionID string) (*protocol.CrewPrimeResult, error) {
	resp, err := c.send(protocol.CrewPrimeMessage{Cmd: protocol.CmdCrewPrime, SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	if resp.CrewPrimeResult == nil {
		return nil, fmt.Errorf("the daemon answered without a priming")
	}
	return resp.CrewPrimeResult, nil
}

func (c *Client) CrewHandoff(sessionID, note string, retry bool, close protocol.CrewDayClose) (*protocol.CrewHandoffResult, error) {
	msg := protocol.CrewHandoffMessage{Cmd: protocol.CmdCrewHandoff, SessionID: sessionID, Note: note}
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

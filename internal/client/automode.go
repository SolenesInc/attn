package client

import "github.com/victorarias/attn/internal/protocol"

// There is no Promote here and there will not be one: promotion is a WebSocket command
// the app sends, because a human in the app is the trust boundary a CLI caller cannot fake.

func (c *Client) AutoModeShow() (*protocol.AutoModeShowResult, error) {
	resp, err := c.send(protocol.AutoModeShowMessage{Cmd: protocol.CmdAutoModeShow})
	if err != nil {
		return nil, err
	}
	return resp.AutomodeShowResult, nil
}

func (c *Client) AutoModeEnvAdd(text string) (*protocol.AutoModeEnvResult, error) {
	resp, err := c.send(protocol.AutoModeEnvAddMessage{Cmd: protocol.CmdAutoModeEnvAdd, Text: text})
	if err != nil {
		return nil, err
	}
	return resp.AutomodeEnvResult, nil
}

func (c *Client) AutoModeEnvRemove(index int) (*protocol.AutoModeEnvResult, error) {
	resp, err := c.send(protocol.AutoModeEnvRemoveMessage{Cmd: protocol.CmdAutoModeEnvRemove, Index: index})
	if err != nil {
		return nil, err
	}
	return resp.AutomodeEnvResult, nil
}

func (c *Client) AutoModePropose(kind, target, value, proposedBy string) (*protocol.AutoModeProposeResult, error) {
	msg := protocol.AutoModeProposeMessage{Cmd: protocol.CmdAutoModePropose, Kind: kind, Value: value}
	if target != "" {
		msg.Target = protocol.Ptr(target)
	}
	if proposedBy != "" {
		msg.ProposedBy = protocol.Ptr(proposedBy)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.AutomodeProposeResult, nil
}

func (c *Client) AutoModeDenials(limit int) (*protocol.AutoModeDenialsResult, error) {
	msg := protocol.AutoModeDenialsMessage{Cmd: protocol.CmdAutoModeDenials}
	if limit > 0 {
		msg.Limit = protocol.Ptr(limit)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.AutomodeDenialsResult, nil
}

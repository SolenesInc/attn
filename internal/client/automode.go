package client

import "github.com/victorarias/attn/internal/protocol"

// Nothing here writes the auto mode config. A human in the app is the trust boundary a
// caller on this socket cannot fake, so every amendment leaves as a proposal.

func (c *Client) AutoModeShow() (*protocol.AutoModeShowResult, error) {
	resp, err := c.send(protocol.AutoModeShowMessage{Cmd: protocol.CmdAutoModeShow})
	if err != nil {
		return nil, err
	}
	return resp.AutomodeShowResult, nil
}

func (c *Client) AutoModeEnvSlot(slot string, values []string) (*protocol.AutoModeEnvResult, error) {
	resp, err := c.send(protocol.AutoModeEnvSlotMessage{
		Cmd:    protocol.CmdAutoModeEnvSlot,
		Slot:   slot,
		Values: values,
	})
	if err != nil {
		return nil, err
	}
	return resp.AutomodeEnvResult, nil
}

func (c *Client) AutoModeEnvNotes(notes []string) (*protocol.AutoModeEnvResult, error) {
	resp, err := c.send(protocol.AutoModeEnvNotesMessage{
		Cmd:   protocol.CmdAutoModeEnvNotes,
		Notes: notes,
	})
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

package client

import "github.com/victorarias/attn/internal/protocol"

// No Promote here and no rule or host add: a human in the app is the trust boundary a
// CLI caller cannot fake, so this surface proposes and the app decides.

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

func (c *Client) AutoModeRuleRemove(pattern []string) (*protocol.AutoModeConfigResult, error) {
	resp, err := c.send(protocol.AutoModeRuleRemoveMessage{
		Cmd:     protocol.CmdAutoModeRuleRemove,
		Pattern: pattern,
	})
	if err != nil {
		return nil, err
	}
	return resp.AutomodeConfigResult, nil
}

func (c *Client) AutoModeHostRemove(host, decision string) (*protocol.AutoModeConfigResult, error) {
	resp, err := c.send(protocol.AutoModeHostRemoveMessage{
		Cmd:      protocol.CmdAutoModeHostRemove,
		Host:     host,
		Decision: decision,
	})
	if err != nil {
		return nil, err
	}
	return resp.AutomodeConfigResult, nil
}

func (c *Client) AutoModePolicySet(approvalPolicy, sandboxMode string) (*protocol.AutoModeConfigResult, error) {
	msg := protocol.AutoModePolicySetMessage{Cmd: protocol.CmdAutoModePolicySet}
	if approvalPolicy != "" {
		msg.ApprovalPolicy = protocol.Ptr(approvalPolicy)
	}
	if sandboxMode != "" {
		msg.SandboxMode = protocol.Ptr(sandboxMode)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.AutomodeConfigResult, nil
}

package client

import (
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) QuickCapture(msg any) (*protocol.QuickCaptureResult, error) {
	response, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if response.QuickCaptureResult == nil {
		return nil, fmt.Errorf("daemon returned no quick capture receipt")
	}
	return response.QuickCaptureResult, nil
}

func (c *Client) AgentInboxEntry(id, sessionID string) (*protocol.AgentPeerMessage, *protocol.AgentInboxItem, error) {
	response, err := c.send(protocol.AgentInboxMessage{Cmd: protocol.CmdAgentInbox, MessageID: protocol.Ptr(id), RecipientSessionID: sessionID})
	if err != nil {
		return nil, nil, err
	}
	if response.AgentInboxResult == nil && response.AgentInboxItemResult == nil {
		return nil, nil, fmt.Errorf("daemon returned no inbox item")
	}
	return response.AgentInboxResult, response.AgentInboxItemResult, nil
}

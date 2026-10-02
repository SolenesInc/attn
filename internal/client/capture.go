package client

import (
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) Capture(msg any) (*protocol.CaptureResult, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if len(data) > protocol.CommandFrameMaxBytes {
		return nil, fmt.Errorf("capture transport limit=%d bytes, asked for %d bytes", protocol.CommandFrameMaxBytes, len(data))
	}
	response, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if response.CaptureResult == nil {
		return nil, fmt.Errorf("daemon returned no capture receipt")
	}
	return response.CaptureResult, nil
}

func (c *Client) AgentInboxEntry(id, recipient string) (*protocol.AgentPeerMessage, *protocol.AgentInboxItem, error) {
	response, err := c.send(protocol.AgentInboxMessage{Cmd: protocol.CmdAgentInbox, MessageID: protocol.Ptr(id), RecipientSessionID: recipient})
	if err != nil {
		return nil, nil, err
	}
	if response.AgentInboxResult == nil && response.AgentInboxItemResult == nil {
		return nil, nil, fmt.Errorf("daemon returned no inbox item")
	}
	return response.AgentInboxResult, response.AgentInboxItemResult, nil
}

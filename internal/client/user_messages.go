package client

import (
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) UserMessage(msg any) (*protocol.UserMessageResult, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if len(data) > protocol.CommandFrameMaxBytes {
		return nil, fmt.Errorf("user message transport limit=%d bytes, asked for %d bytes", protocol.CommandFrameMaxBytes, len(data))
	}
	response, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if response.UserMessageResult == nil {
		return nil, fmt.Errorf("daemon returned no user message receipt")
	}
	return response.UserMessageResult, nil
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

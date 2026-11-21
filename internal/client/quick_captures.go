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

func (c *Client) AgentInboxEntry(id string, sessionID protocol.SessionID) (*protocol.AgentPeerMessage, *protocol.AgentInboxItem, error) {
	var result protocol.AgentInboxBatchResult
	if err := c.RunCommand("agent inbox", sessionID, map[string]any{"message": id}, &result); err != nil {
		return nil, nil, err
	}
	if result.Message == nil && result.Item == nil {
		return nil, nil, fmt.Errorf("daemon returned no inbox item")
	}
	return result.Message, result.Item, nil
}

package client

import (
	"errors"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) KeptConversationList(includeDeleted bool) (*protocol.KeptConversationListResult, error) {
	resp, err := c.send(protocol.KeptConversationListMessage{Cmd: protocol.CmdKeptConversationList, IncludeDeleted: protocol.Ptr(includeDeleted)})
	if err != nil {
		return nil, err
	}
	if resp.KeptConversationListResult == nil {
		return nil, errors.New("daemon returned no conversation list")
	}
	return resp.KeptConversationListResult, nil
}
func (c *Client) KeptConversationKeep(id string, keep bool) error {
	_, err := c.send(protocol.KeptConversationKeepMessage{Cmd: protocol.CmdKeptConversationKeep, SessionID: id, Keep: keep})
	return err
}
func (c *Client) KeptConversationForget(id string) error {
	_, err := c.send(protocol.KeptConversationForgetMessage{Cmd: protocol.CmdKeptConversationForget, SessionID: id})
	return err
}

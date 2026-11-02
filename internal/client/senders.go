package client

import (
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) SenderPair(name string, waiting func(protocol.SenderPairingRequest)) (*protocol.PairedSenderToken, error) {
	frame, err := c.marshalFrame(protocol.SenderPairMessage{Cmd: protocol.CmdSenderPair, Name: name})
	if err != nil {
		return nil, err
	}
	conn, err := c.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.Write(frame); err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}
	decoder := json.NewDecoder(conn)
	read := func() (*protocol.Response, error) {
		var resp protocol.Response
		if err := decoder.Decode(&resp); err != nil {
			return nil, fmt.Errorf("receive response: %w", err)
		}
		noteReply(resp.Ok, resp.Refusal)
		if !resp.Ok {
			return nil, &DaemonError{Code: protocol.Deref(resp.ErrorCode), Message: protocol.Deref(resp.Error)}
		}
		return &resp, nil
	}
	first, err := read()
	if err != nil {
		return nil, err
	}
	if first.SenderPairing == nil {
		return nil, fmt.Errorf("daemon returned no pairing request")
	}
	waiting(*first.SenderPairing)
	second, err := read()
	if err != nil {
		return nil, err
	}
	if second.PairedSenderToken == nil {
		return nil, fmt.Errorf("daemon returned no sender token")
	}
	return second.PairedSenderToken, nil
}

func (c *Client) PairedSenders() ([]protocol.PairedSender, error) {
	resp, err := c.send(protocol.SenderListMessage{Cmd: protocol.CmdSenderList})
	if err != nil {
		return nil, err
	}
	return resp.PairedSenders, nil
}

func (c *Client) UnpairSender(sender string) error {
	_, err := c.send(protocol.SenderUnpairMessage{Cmd: protocol.CmdSenderUnpair, Sender: sender})
	return err
}

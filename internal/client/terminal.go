package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

func TerminalCommand[R any](ctx context.Context, c *Client, name string, session protocol.SessionID, args any) (*R, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	msg := protocol.RunCommandMessage{Cmd: protocol.CmdRunCommand, Command: name, SessionID: protocol.Ptr(session)}
	if err := json.Unmarshal(raw, &msg.Args); err != nil {
		return nil, err
	}
	conn, err := c.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := c.writeFrame(conn, msg); err != nil {
		return nil, err
	}
	var response protocol.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("terminal command: %w", err)
	}
	noteReply(response.Ok, response.Refusal)
	if !response.Ok {
		if refusal := response.CommandRefusal; refusal != nil {
			return nil, &DaemonError{Code: refusal.Code, Message: refusal.Message}
		}
		return nil, fmt.Errorf("%s", protocol.Deref(response.Error))
	}
	raw, err = json.Marshal(response.CommandResult)
	if err != nil {
		return nil, err
	}
	var result R
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

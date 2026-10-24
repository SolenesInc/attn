package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) TerminalCommand(ctx context.Context, message any) (*protocol.Response, error) {
	conn, err := c.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := c.writeFrame(conn, message); err != nil {
		return nil, err
	}
	var response protocol.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("terminal command: %w", err)
	}
	if !response.Ok {
		return nil, fmt.Errorf("%s", protocol.Deref(response.Error))
	}
	return &response, nil
}

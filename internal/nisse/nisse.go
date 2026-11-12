package nisse

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/victorarias/attn/internal/effectrpc"
)

const Protocol = 7

func Socket(home string) string { return filepath.Join(home, "nisse.sock") }

func Connect(ctx context.Context, socket, client, version string) (*effectrpc.Client, error) {
	c, err := effectrpc.Dial(ctx, socket, "nisse")
	if err != nil {
		return nil, err
	}
	var hello struct {
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := c.Call(ctx, "initialize", map[string]any{"client": client, "version": version, "protocol": Protocol}, &hello); err != nil {
		c.Close()
		return nil, err
	}
	if hello.Protocol < Protocol {
		c.Close()
		return nil, fmt.Errorf("nisse %s speaks protocol %d; attn needs %d or later", hello.Version, hello.Protocol, Protocol)
	}
	return c, nil
}

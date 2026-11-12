package nisse

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/victorarias/attn/internal/effectrpc"
)

const Protocol = 7

func Socket(home string) string { return filepath.Join(home, "nisse.sock") }

func Connect(ctx context.Context, socket, client, version string) (*effectrpc.Client, effectrpc.ServerInfo, error) {
	c, err := effectrpc.Dial(ctx, socket, "nisse")
	if err != nil {
		return nil, effectrpc.ServerInfo{}, err
	}
	var hello effectrpc.ServerInfo
	if err := c.Call(ctx, "initialize", map[string]any{"client": client, "version": version, "protocol": Protocol}, &hello); err != nil {
		c.Close()
		return nil, effectrpc.ServerInfo{}, err
	}
	if err := Validate(hello, socket); err != nil {
		c.Close()
		return nil, hello, err
	}
	return c, hello, nil
}

func Validate(hello effectrpc.ServerInfo, _ string) error {
	if hello.Protocol < Protocol {
		return fmt.Errorf("nisse %s speaks protocol %d; attn needs %d or later", hello.Version, hello.Protocol, Protocol)
	}
	return nil
}

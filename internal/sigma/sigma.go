package sigma

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/victorarias/attn/internal/effectrpc"
)

const (
	Server   = "sigma"
	Protocol = 1
)

func Socket(home string) string { return filepath.Join(home, "sigma.sock") }

func Connect(ctx context.Context, socket, client, version string) (*effectrpc.Client, effectrpc.ServerInfo, error) {
	c, err := effectrpc.Dial(ctx, socket, Server)
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

func Validate(hello effectrpc.ServerInfo, socket string) error {
	if hello.Server != Server || hello.Protocol != Protocol {
		return fmt.Errorf("the server at %s is %q %s speaking protocol %d; attn needs %s protocol %d", socket, hello.Server, hello.Version, hello.Protocol, Server, Protocol)
	}
	return nil
}

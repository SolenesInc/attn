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

func Connect(ctx context.Context, socket, client, version string) (*effectrpc.Client, error) {
	c, err := effectrpc.Dial(ctx, socket, Server)
	if err != nil {
		return nil, err
	}
	var hello struct {
		Server   string `json:"server"`
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := c.Call(ctx, "initialize", map[string]any{"client": client, "version": version, "protocol": Protocol}, &hello); err != nil {
		c.Close()
		return nil, err
	}
	if hello.Server != Server || hello.Protocol != Protocol {
		c.Close()
		return nil, fmt.Errorf("the server at %s is %q %s speaking protocol %d; attn needs %s protocol %d", socket, hello.Server, hello.Version, hello.Protocol, Server, Protocol)
	}
	return c, nil
}

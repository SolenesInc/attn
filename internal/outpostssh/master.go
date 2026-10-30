package outpostssh

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/bridge"
)

type Client struct{ Destination, ControlPath, Instance string }

const masterIDPrefixBytes = 12

func For(destination, controlPath, instance string) *Client {
	return &Client{destination, controlPath, instance}
}

type Error struct {
	ExitCode int
	Stderr   string
}

func (e *Error) Error() string { return strings.TrimSpace(e.Stderr) }
func (e *Error) Connectable() bool {
	if e.ExitCode != 255 {
		return false
	}
	for _, sample := range []string{"permission denied", "host key verification failed", "remote host identification has changed", "too many authentication failures"} {
		if strings.Contains(strings.ToLower(e.Stderr), sample) {
			return true
		}
	}
	return false
}

func ControlRoot() string {
	if os.Getenv("ATTN_HARNESS_DATA_DIR") != "" && os.Getenv("ATTN_HARNESS_CONTROL_ROOT") != "" {
		return os.Getenv("ATTN_HARNESS_CONTROL_ROOT")
	}
	return filepath.Join("/tmp", "attn-"+strconv.Itoa(os.Getuid()))
}
func ControlPath(instance, id string) string {
	if instance == "" {
		instance = "default"
	}
	return filepath.Join(ControlRoot(), instance+"-"+strings.ReplaceAll(id, "-", "")[:masterIDPrefixBytes]+".sock")
}
func (c *Client) options(batch bool) []string {
	args := []string{"-o", "ControlPath=" + c.ControlPath, "-o", "RemoteCommand=none", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no"}
	if batch {
		args = append(args, "-o", "BatchMode=yes")
	}
	return args
}
func (c *Client) masterCommand(batch bool) []string {
	args := append([]string{"ssh"}, c.options(batch)...)
	return append(args, "-o", "ControlMaster=yes", "-o", "ControlPersist=yes", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4", "-N", "-T", "-a", "-x", c.Destination)
}
func (c *Client) MasterCommand() []string { return c.masterCommand(false) }
func (c *Client) control(ctx context.Context, action string) error {
	args := append(c.options(true), "-O", action, c.Destination)
	_, err := run(ctx, exec.CommandContext(ctx, "ssh", args...))
	return err
}
func (c *Client) EnsureMaster(ctx context.Context) error {
	if _, _, err := net.SplitHostPort(c.Destination); err == nil && !strings.HasPrefix(c.Destination, "ssh://") {
		return &bridge.Refusal{Reason: "destination", Detail: fmt.Sprintf("added by bridge address %s; remove it and add it again by the name you use with ssh", c.Destination)}
	}
	if err := c.control(ctx, "check"); err == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.ControlPath), 0700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(c.ControlPath), 0700); err != nil {
		return err
	}
	if err := os.Remove(c.ControlPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	argv := c.masterCommand(true)
	_, err := run(ctx, exec.CommandContext(ctx, argv[0], argv[1:]...))
	return err
}
func (c *Client) ExitMaster(ctx context.Context) error { return c.control(ctx, "exit") }
func ExitInstanceMasters(ctx context.Context, instance string) error {
	if instance == "" {
		instance = "default"
	}
	paths, err := filepath.Glob(filepath.Join(ControlRoot(), instance+"-"+strings.Repeat("?", masterIDPrefixBytes)+".sock"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := For("unused", path, instance).ExitMaster(ctx); err != nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove stale master %s: %w", path, err)
			}
		}
	}
	return nil
}

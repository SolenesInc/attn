package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/outpostssh"
)

func runBridge() {
	if err := relayBridge(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func relayBridge(ctx context.Context) error {
	if runtime.GOOS == "linux" {
		enabled, err := outpostssh.KillUserProcesses(ctx)
		if err != nil {
			return err
		}
		if enabled {
			return fmt.Errorf("logind KillUserProcesses is on; outpost agents would die when ssh disconnects; turn KillUserProcesses off before adding this outpost")
		}
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err = daemonctl.Ensure(ctx, binary); err != nil {
		return err
	}
	socket, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: filepath.Join(config.DataDir(), "bridge.sock"), Net: "unix"})
	if err != nil {
		return err
	}
	defer socket.Close()
	input := make(chan error, 1)
	go func() { _, err := io.Copy(socket, os.Stdin); _ = socket.CloseWrite(); input <- err }()
	_, err = io.Copy(os.Stdout, socket)
	_ = socket.CloseRead()
	_ = os.Stdout.Close()
	select {
	case inputErr := <-input:
		if err == nil {
			err = inputErr
		}
	default:
	}
	return err
}

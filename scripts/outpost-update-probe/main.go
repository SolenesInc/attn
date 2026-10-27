package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/bridge"
)

func main() {
	address := flag.String("address", "", "outpost address:port")
	identity := flag.String("identity", "", "JSON bridge identity of the home")
	binary := flag.String("binary", "", "binary to send")
	flag.Parse()
	if *address == "" || *identity == "" || *binary == "" {
		fail(fmt.Errorf("--address, --identity and --binary are required"))
	}
	data, err := os.ReadFile(*identity)
	if err != nil {
		fail(err)
	}
	var self bridge.Identity
	if err := json.Unmarshal(data, &self); err != nil {
		fail(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	start := time.Now()
	conn, _, err := bridge.DialUpdate(ctx, bridge.DialConfig{Address: *address, Self: self, AgentSocket: os.Getenv("SSH_AUTH_SOCK")})
	if err != nil {
		fail(err)
	}
	defer conn.Close()
	out := json.NewEncoder(os.Stdout)
	_ = out.Encode(map[string]any{"seconds": time.Since(start).Seconds(), "target": conn.Target(), "peer": conn.Peer()})
	if err := conn.Send(ctx, self.Build, *binary, func(phase string) {
		_ = out.Encode(map[string]any{"seconds": time.Since(start).Seconds(), "phase": phase})
	}); err != nil {
		fail(err)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

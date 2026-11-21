package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/reading"
)

func runWithoutDaemon() {
	layer, err := command.WithoutDaemon(reading.Handlers())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
	var msg protocol.RunCommandMessage
	if err := json.NewDecoder(os.Stdin).Decode(&msg); err != nil {
		fmt.Fprintln(os.Stderr, "_run-command: read the run_command frame from stdin:", err)
		exit(2)
	}
	args, err := json.Marshal(msg.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "_run-command:", err)
		exit(2)
	}
	result, refusal := layer.Run(context.Background(), command.Caller{}, msg.Command, args)
	if err := json.NewEncoder(os.Stdout).Encode(command.Response(result, refusal)); err != nil {
		fmt.Fprintln(os.Stderr, "_run-command: write the reply:", err)
		exit(1)
	}
}

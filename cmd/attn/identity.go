package main

import (
	"fmt"
	"os"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

func ownTerminalID() protocol.TerminalID {
	return protocol.TrimID(harness.TerminalFromEnv())
}

func currentSession(c *client.Client) (protocol.SessionID, error) {
	terminalID := ownTerminalID()
	if terminalID == "" {
		return "", nil
	}
	_, sessionID, err := c.QueryAs(terminalID)
	if err != nil {
		return "", err
	}
	if sessionID == "" {
		return "", fmt.Errorf("terminal %s is not showing a session", terminalID)
	}
	return sessionID, nil
}

func currentSessionOrExit() protocol.SessionID {
	sessionID, err := currentSession(client.New(""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn: resolve current session: %v\n", err)
		os.Exit(1)
	}
	return sessionID
}

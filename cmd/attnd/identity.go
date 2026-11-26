package main

import (
	"errors"
	"fmt"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/builtin"
	"github.com/victorarias/attn/internal/protocol"
)

var errNoCurrentSession = errors.New("not showing a session")

func currentSession(c *client.Client) (protocol.SessionID, error) {
	terminalID, err := harness.ResolveTerminalFromEnv(builtin.CallerEnvs()...)
	if err != nil {
		return "", err
	}
	if terminalID == "" {
		return "", nil
	}
	sessionID, err := c.CallerSession(terminalID)
	if err != nil {
		return "", err
	}
	if sessionID == "" {
		return "", fmt.Errorf("terminal %s is %w", terminalID, errNoCurrentSession)
	}
	return sessionID, nil
}

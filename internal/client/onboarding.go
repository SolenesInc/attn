package client

import (
	"fmt"
	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) Onboarding(action protocol.OnboardingAction, session protocol.SessionID, profile string) (*protocol.OnboardingResult, error) {
	response, err := c.send(protocol.OnboardingMessage{Cmd: protocol.CmdOnboarding, Action: action, SourceSessionID: protocol.Ptr(session), ProfileID: protocol.Ptr(profile)})
	if err != nil {
		return nil, err
	}
	if response.OnboardingResult == nil {
		return nil, fmt.Errorf("daemon returned no onboarding result")
	}
	return response.OnboardingResult, nil
}

package client

import (
	"errors"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) Onboarding(action protocol.OnboardingAction) (*protocol.OnboardingResult, error) {
	response, err := c.send(protocol.OnboardingMessage{Cmd: protocol.CmdOnboarding, Action: action})
	if err != nil {
		return nil, err
	}
	if response.OnboardingResult == nil {
		return nil, errors.New("daemon returned no onboarding result")
	}
	return response.OnboardingResult, nil
}

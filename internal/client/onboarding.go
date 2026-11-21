package client

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) Onboarding(action protocol.OnboardingAction) (*protocol.OnboardingResult, error) {
	name := strings.TrimSuffix("onboarding "+string(action), " "+string(protocol.OnboardingActionShow))
	var result protocol.OnboardingResult
	if err := c.RunCommand(name, "", struct{}{}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

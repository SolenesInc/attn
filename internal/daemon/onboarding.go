package daemon

import (
	"context"
	"fmt"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) onboardingCommands() map[string]command.Handler {
	run := func(action protocol.OnboardingAction) command.Handler {
		return command.Typed(func(_ context.Context, c command.Caller, _ struct{}) (any, error) {
			result, err := d.onboarding(c, action)
			if err != nil {
				return nil, fmt.Errorf("onboarding: %w", err)
			}
			return result, nil
		})
	}
	return map[string]command.Handler{
		"onboarding":       run(protocol.OnboardingActionShow),
		"onboarding done":  run(protocol.OnboardingActionDone),
		"onboarding again": run(protocol.OnboardingActionAgain),
	}
}

func (d *Daemon) onboarding(c command.Caller, action protocol.OnboardingAction) (*protocol.OnboardingResult, error) {
	var mutate func(*crew.Member)
	switch action {
	case protocol.OnboardingActionShow:
	case protocol.OnboardingActionDone:
		mutate = func(member *crew.Member) { member.FirstDay = nil }
	case protocol.OnboardingActionAgain:
		mutate = func(member *crew.Member) {
			if member.FirstDay == nil {
				member.FirstDay = &crew.FirstDay{}
			}
			member.FirstDay.Onboarding = protocol.OnboardingPathFull
		}
	}
	chief, err := d.crewAskedMember(crewAsker{session: c.Session, profileID: c.Profile}, crew.ChiefAlias)
	if err != nil {
		return nil, err
	}
	if mutate != nil {
		chief, err = d.updateCrewMember(chief.ID, func(member *crew.Member) (bool, error) {
			mutate(member)
			return true, nil
		})
		if err != nil {
			return nil, err
		}
		d.logf("chief: %s ran onboarding %s for %s in profile %s", c.Session, action, chief.Name, chief.ProfileID)
	}
	result := &protocol.OnboardingResult{Chief: chief.Name, Configured: chief.Agent != ""}
	if path, pending := chief.PendingOnboarding(); pending {
		result.Pending = protocol.Ptr(path)
	}
	return result, nil
}

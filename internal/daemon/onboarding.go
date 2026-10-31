package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

var errOnboardingNeedsV2 = errors.New("onboarding belongs to the Chief, which is a crew member only with crew v2 on; `attn settings set crew_v2_enabled true` turns it on")

func (d *Daemon) handleOnboarding(conn net.Conn, msg *protocol.OnboardingMessage) {
	result, err := d.onboarding(msg)
	if err != nil {
		d.sendError(conn, "onboarding: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, OnboardingResult: result})
}

func (d *Daemon) onboarding(msg *protocol.OnboardingMessage) (*protocol.OnboardingResult, error) {
	if !d.crewV2Enabled() {
		return nil, errOnboardingNeedsV2
	}
	var mutate func(*crew.Member)
	switch msg.Action {
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
	default:
		return nil, fmt.Errorf("unknown action %q; use show, done or again", msg.Action)
	}
	chief, err := d.crewAskedMember(askerOf(msg.SourceSessionID, msg.ProfileID), crew.ChiefAlias)
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
		d.logf("chief: %s ran onboarding %s for %s in profile %s", protocol.Deref(msg.SourceSessionID), msg.Action, chief.Name, chief.ProfileID)
	}
	result := &protocol.OnboardingResult{Chief: chief.Name, Configured: chief.Agent != ""}
	if path, pending := chief.PendingOnboarding(); pending {
		result.Pending = protocol.Ptr(path)
	}
	return result, nil
}

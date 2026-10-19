package daemon

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

func chiefFirstDay(member crew.Member) protocol.FirstDay {
	if member.Onboarded {
		return protocol.FirstDayNone
	}
	return member.OnboardingPath
}

func (d *Daemon) handleOnboarding(conn net.Conn, msg *protocol.OnboardingMessage) {
	switch msg.Action {
	case protocol.OnboardingActionShow, protocol.OnboardingActionDone, protocol.OnboardingActionAgain:
	default:
		d.sendError(conn, fmt.Sprintf("unknown onboarding action %q; use show, done or again", msg.Action))
		return
	}
	bindings, err := d.bindings()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	request, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, bindings)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	key, err := d.chief(request.ProfileID())
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	schema, err := d.crewCollection()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	for {
		member, doc, err := d.crewMember(key)
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		if msg.Action != protocol.OnboardingActionShow {
			member.Onboarded = msg.Action == protocol.OnboardingActionDone
			member.OnboardingPath = ""
			if !member.Onboarded {
				member.OnboardingPath = protocol.FirstDayFull
			}
			if _, err := d.writeCrewMember(*schema, member, doc.Rev); err != nil {
				if docstore.IsConflict(err) {
					continue
				}
				d.sendError(conn, err.Error())
				return
			}
		}
		result := protocol.OnboardingResult{Chief: d.memberName(key), Configured: member.Agent != "", Onboarded: member.Onboarded, FirstDay: chiefFirstDay(member)}
		_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, OnboardingResult: &result})
		return
	}
}

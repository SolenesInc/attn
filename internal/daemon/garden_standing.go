package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

type gardenStanding struct {
	daemon    *Daemon
	profileID string
	live      func(protocol.SessionID) bool
}

func (s gardenStanding) Holds(p who.Party) bool {
	if id, ok := p.Session(); ok {
		return s.live(id)
	}
	return s.daemon.partyHolds(p)
}

func (s gardenStanding) Name(p who.Party) string { return s.daemon.partyName(p) }

func (s gardenStanding) Oversees(actor who.Actor, held who.Party) bool {
	asking, ok := s.daemon.sessionOfActor(actor)
	if !ok || !s.daemon.sessionActive(asking) {
		return false
	}
	caller := s.daemon.store.Get(asking)
	if caller == nil || caller.ProfileID != s.profileID {
		return false
	}
	if s.daemon.chiefOfProfile(s.profileID) == asking {
		return true
	}
	tender, ok := s.daemon.liveSessionOf(held)
	if !ok {
		return false
	}
	target := s.daemon.store.Get(tender)
	return target != nil && s.daemon.mayActOnSession(asking, target, "move a seed tended by") == nil
}

func (d *Daemon) seedMoveAuditBody(seed garden.Seed, verb garden.Verb, actor who.Actor, displaced who.Party, by garden.Means) string {
	if by == garden.MeansForce {
		return d.forcedSeedMoveBody(seed.ID, verb, actor, displaced)
	}
	standing := "delegation-chain standing"
	if asking, ok := d.sessionOfActor(actor); ok && d.chiefOfProfile(seed.ProfileID) == asking {
		standing = "chief standing"
	}
	return fmt.Sprintf("%s used %s for `attn seed %s %s`; %s's claim ended.", d.actorName(actor), standing, verb, seed.ID, d.partyName(displaced))
}

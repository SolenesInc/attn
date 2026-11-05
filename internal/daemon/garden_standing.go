package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/who"
)

type gardenStanding struct {
	daemon           *Daemon
	profileID        string
	releasesSessions bool
}

func (s gardenStanding) ClaimHolds(seed garden.Seed) bool {
	if _, session := seed.Tender.Session(); session && s.releasesSessions {
		return false
	}
	return s.daemon.seedClaimHolds(seed)
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
	tender, ok := held.Session()
	if !ok {
		tender, ok = s.daemon.liveSessionOf(held)
	}
	return ok && (tender == asking || s.daemon.inDelegationChain(asking, caller.ProfileID, tender))
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

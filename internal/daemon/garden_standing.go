package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

type gardenStanding struct {
	daemon    *Daemon
	profileID string
	live      func(protocol.SessionID) bool
}

func (s gardenStanding) Live(id protocol.SessionID) bool { return s.live(id) }

func (s gardenStanding) Oversees(actor, held garden.Tender) bool {
	if actor.Session == "" || !s.daemon.sessionActive(actor.Session) {
		return false
	}
	caller := s.daemon.store.Get(actor.Session)
	if caller == nil || caller.ProfileID != s.profileID {
		return false
	}
	if held.Session == "" {
		return s.daemon.chiefOfProfile(s.profileID) == actor.Session
	}
	target := s.daemon.store.Get(held.Session)
	return target != nil && s.daemon.mayActOnSession(actor.Session, target, "move a seed tended by") == nil
}

func (d *Daemon) seedMoveAuditBody(seed garden.Seed, verb garden.Verb, actor, displaced garden.Tender, by garden.Means) string {
	if by == garden.MeansForce {
		return forcedSeedMoveBody(seed.ID, verb, actor, displaced)
	}
	standing := "delegation-chain standing"
	if d.chiefOfProfile(seed.ProfileID) == actor.Session {
		standing = "chief standing"
	}
	return fmt.Sprintf("%s used %s for `attn seed %s %s`; %s's claim ended.", actor.DisplayName(), standing, verb, seed.ID, displaced.DisplayName())
}

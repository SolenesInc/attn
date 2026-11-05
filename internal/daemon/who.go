package daemon

import (
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) actorOfSession(id protocol.SessionID) who.Actor {
	if id = protocol.TrimID(id); id == "" {
		return who.User()
	}
	return d.partyOfSession(id).Actor()
}

func (d *Daemon) partyHolds(p who.Party) bool {
	return d.partiesNow().holds(p)
}

func (d *Daemon) seedClaimHolds(seed garden.Seed) bool {
	return d.partiesNow().claimHolds(seed)
}

func (d *Daemon) partyView(p who.Party) protocol.PartyView {
	return d.partiesNow().view(p)
}

func (d *Daemon) actorView(a who.Actor) protocol.ActorView {
	return d.partiesNow().actor(a)
}

func (d *Daemon) partyName(p who.Party) string {
	if key, member := p.Member(); member {
		return d.crewMemberName(key.String())
	}
	id, _ := p.Session()
	return string(id)
}

func (d *Daemon) actorName(a who.Actor) string {
	if p, ok := a.Party(); ok {
		return d.partyName(p)
	}
	return actorName(a)
}

func actorName(a who.Actor) string {
	switch a {
	case who.User():
		return "the user"
	case who.Attn():
		return "attn"
	}
	return a.String()
}

func (d *Daemon) sessionOfActor(a who.Actor) (protocol.SessionID, bool) {
	p, ok := a.Party()
	if !ok {
		return "", false
	}
	return d.liveSessionOf(p)
}

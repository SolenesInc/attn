package daemon

import (
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
	if id, ok := p.Session(); ok {
		return d.sessionActive(id)
	}
	_, ok := p.Member()
	return ok
}

func (d *Daemon) actorView(a who.Actor) protocol.ActorView {
	if p, ok := a.Party(); ok {
		view := d.partyView(p)
		return protocol.ActorView{Ref: protocol.ActorRef(view.Ref), Name: view.Name}
	}
	return protocol.ActorView{Ref: protocol.ActorRef(a.String()), Name: actorName(a)}
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

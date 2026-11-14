package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func appCaller() who.Actor { return who.User() }

func phoneCaller(HomeGatewayPhone) who.Actor { return who.User() }

type socketCaller struct {
	daemon  *Daemon
	session protocol.SessionID
}

func (c socketCaller) Actor() who.Actor { return c.daemon.sessionOrOwner(c.session) }

func (d *Daemon) delegationCaller(accepted delegationTarget, source *protocol.SessionID) who.Actor {
	if !accepted.By.IsZero() {
		return accepted.By
	}
	return d.sessionOrOwner(protocol.TrimID(protocol.Deref(source)))
}

func (d *Daemon) delegationDispatcher(source protocol.SessionID, by who.Actor) who.Actor {
	if source = protocol.TrimID(source); source != "" {
		return d.partyOfSession(source).Actor()
	}
	return by
}

func (d *Daemon) sessionOrOwner(session protocol.SessionID) who.Actor {
	if session == "" {
		return who.User()
	}
	return d.partyOfSession(session).Actor()
}

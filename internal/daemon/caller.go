package daemon

import (
	"context"
	"encoding/json"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func appCaller() who.Actor { return who.User() }

func phoneCaller(HomeGatewayPhone) who.Actor { return who.User() }

func (d *Daemon) socketCaller(ctx context.Context, frame []byte) who.Actor {
	var declared struct {
		Source protocol.SessionID `json:"source_session_id"`
	}
	_ = json.Unmarshal(frame, &declared)
	if source := protocol.TrimID(declared.Source); source != "" {
		return d.sessionOrOwner(source)
	}
	return d.sessionOrOwner(d.callerSession(ctx, frame))
}

func (d *Daemon) delegationCaller(accepted delegationTarget, source *protocol.SessionID) who.Actor {
	if !accepted.By.IsZero() {
		return accepted.By
	}
	return d.sessionOrOwner(protocol.TrimID(protocol.Deref(source)))
}

func (d *Daemon) sessionOrOwner(session protocol.SessionID) who.Actor {
	if session == "" {
		return who.User()
	}
	return d.partyOfSession(session).Actor()
}

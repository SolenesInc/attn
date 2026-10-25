package daemon

import (
	"syscall"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type sessionCloseInFlight struct {
	teardown *sessionTeardown
}

func (d *Daemon) beginSessionCloseAsUser(sessionID protocol.SessionID, closed store.SessionClose, client *wsClient) (sessionCloseInFlight, error) {
	if err := d.requireSessionHost(sessionID); err != nil {
		return sessionCloseInFlight{}, err
	}
	if err := d.sessionCloseError(sessionID); err != nil {
		d.logf("refusing to close protected session %s: %v", sessionID, err)
		return sessionCloseInFlight{}, err
	}
	return d.beginSessionClose(sessionID, closed, client)
}

func (d *Daemon) beginSessionClose(
	sessionID protocol.SessionID, closed store.SessionClose, client *wsClient,
) (sessionCloseInFlight, error) {
	teardown, err := d.prepareSessionTeardown(sessionID)
	if err != nil {
		return sessionCloseInFlight{}, err
	}
	d.commitSessionUnregister(sessionID, closed)
	if client != nil {
		for _, terminal := range teardown.terminals {
			d.detachSession(client, terminal)
		}
	}
	if teardown != nil && teardown.session != nil {
		d.publishSessionUnregistered(teardown.session)
		d.publishFact(FactSessionTerminated, string(teardown.session.ID), nil)
	}
	return sessionCloseInFlight{teardown: teardown}, nil
}

func (d *Daemon) finishSessionClose(sessionID protocol.SessionID, closing sessionCloseInFlight) {
	if closing.teardown != nil {
		done := d.terminateSessionAsync(sessionID, syscall.SIGTERM, closing.teardown)
		if d.sessionHost(sessionID) != "" {
			<-done
		}
	}
}

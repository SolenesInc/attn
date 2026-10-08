package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type sessionCloseInFlight struct {
	teardown *sessionTeardown
}

func (d *Daemon) beginSessionCloseAsUser(sessionID protocol.SessionID, closed store.SessionClose, client *wsClient) (sessionCloseInFlight, error) {
	if err := d.sessionCloseError(sessionID); err != nil {
		d.logf("refusing to unregister protected session %s: %v", sessionID, err)
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
	if endpointID, remote := d.sessionOwningEndpoint(sessionID); remote {
		if err := d.forwardSessionClose(endpointID, sessionID, closed); err != nil {
			d.cancelSessionTeardown(sessionID, teardown)
			return sessionCloseInFlight{}, err
		}
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
		d.terminateSessionAsync(sessionID, syscall.SIGTERM, closing.teardown)
	}
}

func (d *Daemon) sessionOwningEndpoint(sessionID protocol.SessionID) (string, bool) {
	if d.hubManager == nil {
		return "", false
	}
	return d.hubManager.EndpointIDForSession(sessionID)
}

func (d *Daemon) forwardSessionClose(endpointID string, sessionID protocol.SessionID, closed store.SessionClose) error {
	msg := protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: sessionID}
	if closed.By != "" && closed.By != store.SessionClosedByUser {
		msg.ClosedBy = protocol.Ptr(closed.By)
	}
	if closed.Reason != "" {
		msg.CloseReason = protocol.Ptr(closed.Reason)
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal the close of session %s for endpoint %s: %w", sessionID, endpointID, err)
	}
	if err := d.hubManager.ForwardSessionClose(context.Background(), endpointID, sessionID, payload); err != nil {
		d.logf("close forward failed for %s on endpoint %s: %v", sessionID, endpointID, err)
		return err
	}
	return nil
}

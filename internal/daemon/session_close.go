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

func (d *Daemon) beginSessionClose(
	sessionID string, closed store.SessionClose, client *wsClient,
) (sessionCloseInFlight, error) {
	teardown, err := d.prepareSessionTeardown(sessionID)
	if err != nil {
		return sessionCloseInFlight{}, err
	}
	// The owning daemon writes the ledger row for a session it owns, so the close
	// is not this daemon's to record until that one has taken it.
	if endpointID, remote := d.sessionOwningEndpoint(sessionID); remote {
		if err := d.forwardSessionClose(endpointID, sessionID, closed); err != nil {
			d.cancelSessionTeardown(sessionID)
			return sessionCloseInFlight{}, err
		}
	}
	d.commitSessionUnregister(sessionID, closed)
	if client != nil {
		d.detachSession(client, sessionID)
	}
	if teardown != nil && teardown.session != nil {
		d.publishSessionUnregistered(teardown.session)
		d.dissociateSessionFromWorkspace(teardown.session.ID)
		d.removeWorkspaceLayoutPaneForSession(teardown.session.ID)
		d.publishFact(FactSessionTerminated, teardown.session.ID, nil)
	}
	return sessionCloseInFlight{teardown: teardown}, nil
}

// finishSessionClose kills the runtime. Split from beginSessionClose so a caller
// can answer its requester before the session it is closing dies.
func (d *Daemon) finishSessionClose(sessionID string, closing sessionCloseInFlight) {
	if closing.teardown != nil {
		d.terminateSessionAsync(sessionID, syscall.SIGTERM, closing.teardown)
	}
}

func (d *Daemon) sessionOwningEndpoint(sessionID string) (string, bool) {
	if d.hubManager == nil {
		return "", false
	}
	return d.hubManager.EndpointIDForSession(sessionID)
}

func (d *Daemon) forwardSessionClose(endpointID, sessionID string, closed store.SessionClose) error {
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

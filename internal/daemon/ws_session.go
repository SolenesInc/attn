package daemon

import (
	"os"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleClearWarningsWS() {
	d.logf("Clearing daemon warnings")
	d.clearWarnings()
}

func unregisterSessionClose(msg *protocol.UnregisterMessage) store.SessionClose {
	closed := store.SessionClose{
		By:     protocol.TrimID(protocol.Deref(msg.ClosedBy)),
		Reason: strings.TrimSpace(protocol.Deref(msg.CloseReason)),
	}
	if closed.By == "" {
		closed.By = store.SessionClosedByUser
	}
	return closed
}

func (d *Daemon) beginUserSessionClose(sessionID protocol.SessionID, closed store.SessionClose, client *wsClient) (sessionCloseInFlight, error) {
	if err := d.sessionCloseError(sessionID); err != nil {
		d.logf("refusing to close protected session %s: %v", sessionID, err)
		return sessionCloseInFlight{}, err
	}
	d.logf("closing session %s for a client", sessionID)
	return d.beginSessionClose(sessionID, closed, client)
}

func (d *Daemon) handleUnregisterWS(client *wsClient, msg *protocol.UnregisterMessage) {
	closing, err := d.beginUserSessionClose(msg.ID, unregisterSessionClose(msg), client)
	if err != nil {
		d.sendCommandError(client, protocol.CmdUnregister, err.Error())
		d.answerSessionClose(client, msg.ID, err)
		return
	}
	d.answerSessionClose(client, msg.ID, nil)
	d.finishSessionClose(msg.ID, closing)
}

func (d *Daemon) answerSessionClose(client *wsClient, sessionID protocol.SessionID, refusal error) {
	answer := &protocol.SessionCloseResultMessage{
		Event:     protocol.EventSessionCloseResult,
		SessionID: sessionID,
		Accepted:  refusal == nil,
	}
	if refusal != nil {
		answer.Error = protocol.Ptr(refusal.Error())
	}
	d.sendToClient(client, answer)
}

func (d *Daemon) handleGetRecentLocationsWS(client *wsClient, msg *protocol.GetRecentLocationsMessage) {
	limit := 20
	if msg.Limit != nil {
		limit = int(*msg.Limit)
	}
	d.logf("Getting recent locations (limit=%d)", limit)
	locations := d.store.GetRecentLocations(limit)
	homePath, _ := os.UserHomeDir()
	d.sendToClient(client, &protocol.RecentLocationsResultMessage{
		Event:           protocol.EventRecentLocationsResult,
		RecentLocations: protocol.RecentLocationsToValues(locations),
		EndpointID:      msg.EndpointID,
		RequestID:       msg.RequestID,
		HomePath:        protocol.Ptr(homePath),
		Success:         true,
	})
}

func (d *Daemon) handleRecentFilesWS(client *wsClient, msg *protocol.RecentFilesMessage) {
	limit := 20
	if msg.Limit != nil {
		limit = int(*msg.Limit)
	}
	d.sendToClient(client, &protocol.RecentFilesResultMessage{
		Event:     protocol.EventRecentFilesResult,
		Files:     d.store.GetRecentFiles(limit, strings.TrimSpace(protocol.Deref(msg.Root))),
		RequestID: strings.TrimSpace(protocol.Deref(msg.RequestID)),
		Success:   true,
	})
}

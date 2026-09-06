package daemon

import (
	"context"
	"os"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleClearSessionsWS() {
	d.logf("Clearing all sessions")
	d.clearAllSessions()
}

func (d *Daemon) handleClearWarningsWS() {
	d.logf("Clearing daemon warnings")
	d.clearWarnings()
}

func unregisterSessionClose(msg *protocol.UnregisterMessage) store.SessionClose {
	closed := store.SessionClose{
		By:     strings.TrimSpace(protocol.Deref(msg.ClosedBy)),
		Reason: strings.TrimSpace(protocol.Deref(msg.CloseReason)),
	}
	if closed.By == "" {
		closed.By = store.SessionClosedByUser
	}
	return closed
}

func (d *Daemon) handleUnregisterWS(client *wsClient, msg *protocol.UnregisterMessage) {
	if closeErr := d.sessionCloseError(msg.ID); closeErr != nil {
		d.logf("refusing to unregister protected session %s: %v", msg.ID, closeErr)
		d.sendCommandError(client, protocol.CmdUnregister, closeErr.Error())
		d.answerSessionClose(client, msg.ID, closeErr)
		return
	}
	d.logf("Unregistering session %s via WebSocket", msg.ID)
	closing, err := d.beginSessionClose(msg.ID, unregisterSessionClose(msg), client)
	if err != nil {
		d.sendCommandError(client, protocol.CmdUnregister, err.Error())
		d.answerSessionClose(client, msg.ID, err)
		return
	}
	d.answerSessionClose(client, msg.ID, nil)
	d.finishSessionClose(msg.ID, closing)
}

// A forwarding hub waits on this, named by session. The broadcast cannot answer
// it: that rides the bus, and it says nothing when the close is refused.
func (d *Daemon) answerSessionClose(client *wsClient, sessionID string, refusal error) {
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

func (d *Daemon) clearAllSessions() {
	sessionIDs := make(map[string]struct{})
	for _, session := range d.store.List("") {
		sessionIDs[session.ID] = struct{}{}
	}

	if d.ptyBackend != nil {
		recoverCtx, cancel := context.WithTimeout(context.Background(), deferredRecoveryRPCTimeout)
		report, err := d.ptyBackend.Recover(recoverCtx)
		cancel()
		if err != nil {
			d.logf("clear_sessions recovery scan failed: %v", err)
		} else if report.Recovered > 0 || report.Pruned > 0 || report.Missing > 0 || report.Failed > 0 {
			d.logf(
				"clear_sessions recovery summary: recovered=%d pruned=%d missing=%d failed=%d",
				report.Recovered,
				report.Pruned,
				report.Missing,
				report.Failed,
			)
		}
		for _, sessionID := range d.liveRuntimeSessionIDs(context.Background()) {
			sessionIDs[sessionID] = struct{}{}
		}
	}

	d.coalesceSnapshots(func() {
		for sessionID := range sessionIDs {
			d.terminateSession(sessionID, syscall.SIGTERM)
		}
		d.store.ClearSessions()
		d.clearChiefOfStaffIfSession(d.chiefOfStaffSessionID())
		for sessionID := range sessionIDs {
			d.publishFact(FactSessionTerminated, sessionID, nil)
		}
	})
}

package daemon

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

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

func (d *Daemon) handleUnregisterWS(client *wsClient, msg *protocol.UnregisterMessage) {
	d.logf("Unregistering session %s via WebSocket", msg.ID)
	closing, err := d.beginSessionCloseAsUser(msg.ID, unregisterSessionClose(msg), client)
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
		RequestID:       msg.RequestID,
		HomePath:        protocol.Ptr(homePath),
		Success:         true,
	})
}

func (d *Daemon) handleRecentFilesWS(client *wsClient, msg *protocol.RecentFilesMessage) {
	d.forgetMissingRecentFiles()
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

const recentFileStatDeadline = 500 * time.Millisecond

func (d *Daemon) forgetMissingRecentFiles() {
	paths := d.store.FileActivityPaths()
	if len(paths) == 0 {
		return
	}
	type statAnswer struct {
		path string
		err  error
	}
	answers := make(chan statAnswer, len(paths))
	pending := make(map[string]struct{}, len(paths))
	stat := d.recentFileStat
	deadline := time.NewTimer(recentFileStatDeadline)
	defer deadline.Stop()
	for _, path := range paths {
		pending[path] = struct{}{}
		go func() {
			_, err := stat(path)
			answers <- statAnswer{path: path, err: err}
		}()
	}
	var missing []string
collect:
	for len(pending) > 0 {
		select {
		case answer := <-answers:
			delete(pending, answer.path)
			if errors.Is(answer.err, fs.ErrNotExist) {
				missing = append(missing, answer.path)
			}
		case <-deadline.C:
			for path := range pending {
				d.logf("recent_files: stat %q exceeded deadline=%s; forgotten", path, recentFileStatDeadline)
				missing = append(missing, path)
			}
			break collect
		}
	}
	d.store.DeleteFileActivity(missing...)
}

package daemon

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) handleClearWarningsWS() {
	d.logf("Clearing daemon warnings")
	d.clearWarnings()
}

func unregisterSessionClose(msg *protocol.UnregisterMessage, by who.Actor) store.SessionClose {
	closed := store.SessionClose{By: by, Reason: strings.TrimSpace(protocol.Deref(msg.CloseReason))}
	if by := protocol.TrimID(protocol.Deref(msg.ClosedBy)); by != "" && by != "user" {
		closed.By = who.Session(protocol.SessionID(by)).Actor()
	}
	return closed
}

func (d *Daemon) handleUnregisterWS(client *wsClient, msg *protocol.UnregisterMessage) {
	d.logf("Unregistering session %s via WebSocket", msg.ID)
	closing, err := d.beginSessionCloseAsUser(msg.ID, unregisterSessionClose(msg, client.caller), client)
	if err != nil {
		d.sendCommandError(client, protocol.CmdUnregister, err.Error())
		d.answerSessionClose(client, msg.ID, err)
		return
	}
	d.answerSessionClose(client, msg.ID, nil)
	if done := d.finishSessionClose(msg.ID, closing); done != nil && d.sessionHost(msg.ID) != "" {
		<-done
	}
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
	locations := d.store.GetRecentLocations(limit, protocol.Deref(msg.HomeOnly))
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
	limit := 20
	if msg.Limit != nil && *msg.Limit > 0 {
		limit = int(*msg.Limit)
	}
	d.sendToClient(client, &protocol.RecentFilesResultMessage{
		Event:     protocol.EventRecentFilesResult,
		Files:     d.openableRecentFiles(limit),
		RequestID: strings.TrimSpace(protocol.Deref(msg.RequestID)),
		Success:   true,
	})
}

const recentFileStatDeadline = 500 * time.Millisecond

// All of a request's checks of one outpost share this. Each batch costs a bridge
// round trip, 35-89ms in the checkout_inspect receipts.
const outpostMissingFilesBudget = time.Second

func (d *Daemon) openableRecentFiles(limit int) []protocol.FileActivity {
	d.recentFilePruneGate <- struct{}{}
	defer func() { <-d.recentFilePruneGate }()
	ranked := d.store.GetRecentFiles()
	byMachine := map[string][]string{}
	for _, file := range ranked {
		host := protocol.Deref(file.Host)
		byMachine[host] = append(byMachine[host], file.Path)
	}
	var mu sync.Mutex
	missing := map[store.HostPath]bool{}
	tasks := make([]func(), 0, len(byMachine))
	for host, paths := range byMachine {
		tasks = append(tasks, func() {
			forgotten := d.forgetMissingFiles(host, paths, limit)
			mu.Lock()
			defer mu.Unlock()
			for _, path := range forgotten {
				missing[store.HostPath{Host: host, Path: path}] = true
			}
		})
	}
	runJoined(tasks...)
	listed := map[string]int{}
	return slices.DeleteFunc(ranked, func(file protocol.FileActivity) bool {
		key := store.HostPath{Host: protocol.Deref(file.Host), Path: file.Path}
		if missing[key] || listed[key.Host] == limit {
			return true
		}
		listed[key.Host]++
		return false
	})
}

func (d *Daemon) forgetMissingFiles(host string, ranked []string, limit int) []string {
	ctx := d.life.Context()
	if host != "" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, outpostMissingFilesBudget)
		defer cancel()
	}
	var missing []string
	batch := limit
	for present := 0; present < limit && len(ranked) > 0; batch *= 2 {
		paths := ranked[:min(batch, len(ranked))]
		ranked = ranked[len(paths):]
		gone := d.findMissingFiles(ctx, host, paths)
		d.store.DeleteFileActivity(host, gone...)
		missing = append(missing, gone...)
		present += len(paths) - len(gone)
	}
	return missing
}

func (d *Daemon) findMissingFiles(ctx context.Context, host string, paths []string) []string {
	ask := protocol.FindMissingFilesMessage{Cmd: protocol.CmdFindMissingFiles, Paths: paths}
	if host != "" {
		ask.Host = protocol.Ptr(host)
	}
	found, err := Call[protocol.MissingFiles](ctx, d.router(), ask)
	if err != nil {
		d.logf("recent_files: %s did not check its files, listed unchecked: %v", host, err)
		return nil
	}
	return found.Missing
}

func (d *Daemon) findMissingFilesHere(_ context.Context, msg protocol.FindMissingFilesMessage) (protocol.MissingFiles, error) {
	type statAnswer struct {
		path string
		err  error
	}
	answers := make(chan statAnswer, len(msg.Paths))
	pending := make(map[string]struct{}, len(msg.Paths))
	stat := d.recentFileStat
	deadline := time.NewTimer(recentFileStatDeadline)
	defer deadline.Stop()
	for _, path := range msg.Paths {
		pending[path] = struct{}{}
		go func() {
			_, err := stat(path)
			answers <- statAnswer{path: path, err: err}
		}()
	}
	missing := []string{}
collect:
	for len(pending) > 0 {
		select {
		case answer := <-answers:
			delete(pending, answer.path)
			if answer.err != nil {
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
	return protocol.MissingFiles{Missing: missing}, nil
}

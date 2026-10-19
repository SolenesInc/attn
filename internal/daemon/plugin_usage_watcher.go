package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

type pluginUsageWatcher struct {
	entry     *monitorEntry
	sessionID protocol.SessionID
	path      string
	stopCh    chan struct{}
	doneCh    chan struct{}
}

func (d *Daemon) ensurePluginUsageWatcher(sessionID protocol.SessionID, agent string, path string) {
	sessionID = protocol.TrimID(sessionID)
	path = strings.TrimSpace(path)
	if sessionID == "" || path == "" || !transcript.SupportsUsage(agent) {
		return
	}

	e := d.monitorEntry(sessionID)
	if e == nil {
		return
	}
	if !d.life.Do("seedPluginUsageBaseline", func() { d.seedPluginUsageBaseline(sessionID, path) }) {
		return
	}
	e.mu.Lock()
	previous := e.pluginUsageWatch
	if e.usageDraining || previous != nil && previous.path == path {
		e.mu.Unlock()
		return
	}
	watcher := &pluginUsageWatcher{entry: e, sessionID: sessionID, path: path, stopCh: make(chan struct{}), doneCh: make(chan struct{})}
	tracker := newSessionUsageTrackerAt(d, e, sessionID, agent, path, transcript.NewReportedUsageSourceResolver(path))
	// Admitted before it replaces the previous watcher, so a refused start leaves that one to stop's final reconcile.
	if !d.life.Go("runPluginUsageWatcher", func() { d.runPluginUsageWatcher(watcher, tracker) }) {
		e.mu.Unlock()
		return
	}
	e.beginUsageRunLocked()
	e.pluginUsageWatch = watcher

	e.mu.Unlock()
	if previous != nil {
		close(previous.stopCh)
	}
	d.logf("plugin usage watcher: started session=%s agent=%s path=%s", sessionID, agent, path)
}

func (d *Daemon) seedPluginUsageBaseline(sessionID protocol.SessionID, path string) {
	if d.store == nil {
		return
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return
	}
	if entry := d.monitorEntry(sessionID); entry != nil {
		entry.initializeCostSources(map[string]string{filepath.Clean(path): ""}, false)
	}
}

func (d *Daemon) runPluginUsageWatcher(w *pluginUsageWatcher, tracker *sessionUsageTracker) {
	defer w.entry.endUsageRun()
	defer close(w.doneCh)
	ticker := time.NewTicker(transcriptPollInterval)
	defer ticker.Stop()

	var read os.FileInfo
	for {
		select {
		case <-w.stopCh:
			d.reconcilePluginUsageOnStop(w.path, read != nil, tracker)
			d.logf("plugin usage watcher: stopped session=%s", w.sessionID)
			return
		case <-d.life.Done():
			seen := read != nil
			w.entry.deferFinalUsage(func() { d.reconcilePluginUsageOnStop(w.path, seen, tracker) })
			return
		case <-ticker.C:
		}

		info, err := os.Stat(w.path)
		if err != nil {
			continue
		}
		if read != nil && info.Size() == read.Size() &&
			info.ModTime().Equal(read.ModTime()) && os.SameFile(info, read) {
			continue
		}
		read = info
		tracker.Reconcile()
	}
}

func (d *Daemon) reconcilePluginUsageOnStop(path string, seen bool, tracker *sessionUsageTracker) {
	if !seen {
		if _, err := os.Stat(path); err != nil {
			return
		}
	}
	tracker.Reconcile()
}

func (d *Daemon) restorePluginUsageWatchers() {
	if d.store == nil {
		return
	}
	for _, run := range d.store.ListActiveAgentDriverRuns() {
		if strings.TrimSpace(run.TranscriptPath) == "" {
			continue
		}
		session := d.store.Get(run.SessionID)
		if session == nil {
			continue
		}
		d.ensurePluginUsageWatcher(run.SessionID, session.Agent, run.TranscriptPath)
	}
}

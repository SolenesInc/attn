package daemon

import (
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/transcript"
)

// A plugin harness has no Go driver, so nothing discovers where it writes: the
// driver reports the path and this watcher prices exactly that file.
type pluginUsageWatcher struct {
	sessionID string
	path      string
	stopCh    chan struct{}
}

func (d *Daemon) ensurePluginUsageWatcher(sessionID, agent, path string) {
	sessionID = strings.TrimSpace(sessionID)
	path = strings.TrimSpace(path)
	if sessionID == "" || path == "" || !transcript.SupportsUsage(agent) {
		return
	}

	d.watchersMu.Lock()
	previous := d.pluginUsageWatch[sessionID]
	if previous != nil && previous.path == path {
		d.watchersMu.Unlock()
		return
	}
	if d.pluginUsageWatch == nil {
		d.pluginUsageWatch = make(map[string]*pluginUsageWatcher)
	}
	watcher := &pluginUsageWatcher{sessionID: sessionID, path: path, stopCh: make(chan struct{})}
	d.pluginUsageWatch[sessionID] = watcher
	d.watchersMu.Unlock()
	if previous != nil {
		close(previous.stopCh)
	}

	tracker := newSessionUsageTrackerAt(d, sessionID, agent, path, transcript.NewReportedUsageSourceResolver(path))
	d.logf("plugin usage watcher: started session=%s agent=%s path=%s", sessionID, agent, path)
	go d.runPluginUsageWatcher(watcher, tracker)
}

func (d *Daemon) runPluginUsageWatcher(w *pluginUsageWatcher, tracker *sessionUsageTracker) {
	ticker := time.NewTicker(transcriptPollInterval)
	defer ticker.Stop()

	var read os.FileInfo
	for {
		select {
		case <-w.stopCh:
			tracker.Reconcile()
			d.logf("plugin usage watcher: stopped session=%s", w.sessionID)
			return
		case <-ticker.C:
		}

		// The stat is the movement gate: accounting opens the transcript only on a
		// byte-length change, so an idle session adds no file reads.
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

func (d *Daemon) stopPluginUsageWatcher(sessionID string) {
	d.watchersMu.Lock()
	watcher, ok := d.pluginUsageWatch[sessionID]
	if ok {
		delete(d.pluginUsageWatch, sessionID)
	}
	d.watchersMu.Unlock()
	if ok {
		close(watcher.stopCh)
	}
}

func (d *Daemon) stopAllPluginUsageWatchers() {
	d.watchersMu.Lock()
	watchers := make([]*pluginUsageWatcher, 0, len(d.pluginUsageWatch))
	for _, watcher := range d.pluginUsageWatch {
		watchers = append(watchers, watcher)
	}
	d.pluginUsageWatch = make(map[string]*pluginUsageWatcher)
	d.watchersMu.Unlock()

	for _, watcher := range watchers {
		close(watcher.stopCh)
	}
}

// A restart loses the watchers, not the reported paths: every live plugin run
// that already named its transcript picks accounting back up where it stopped.
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
		d.ensurePluginUsageWatcher(run.SessionID, string(session.Agent), run.TranscriptPath)
	}
}

package sessionmonitor

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/transcript"
)

type pluginUsageWatcher struct {
	entry  *Entry
	path   string
	stopCh chan struct{}
	doneCh chan struct{}
}

func (e *Entry) WatchDriverUsage(path string) {
	if e == nil {
		return
	}
	path = strings.TrimSpace(path)
	if e == nil || path == "" {
		return
	}
	e.mu.Lock()
	agent := string(e.agent)
	e.mu.Unlock()
	if !transcript.SupportsUsage(agent) {
		return
	}
	host := e.registry.host
	if !host.Do("seedPluginUsageBaseline", func() { e.SeedDriverUsageBaseline(path) }) {
		return
	}
	e.mu.Lock()
	previous := e.pluginUsageWatch
	if e.usageDraining || previous != nil && previous.path == path {
		e.mu.Unlock()
		return
	}
	watcher := &pluginUsageWatcher{entry: e, path: path, stopCh: make(chan struct{}), doneCh: make(chan struct{})}
	tracker := newUsageTrackerAt(e, agent, path, transcript.NewReportedUsageSourceResolver(path))
	// Admitted before it replaces the previous watcher, so a refused start leaves that one to stop's final reconcile.
	if !host.Go("runPluginUsageWatcher", func() { watcher.run(tracker) }) {
		e.mu.Unlock()
		return
	}
	e.beginUsageRunLocked()
	e.pluginUsageWatch = watcher
	label := e.label
	e.mu.Unlock()
	if previous != nil {
		close(previous.stopCh)
	}
	e.logf("plugin usage watcher: started session=%s agent=%s path=%s", label, agent, path)
}

func (e *Entry) SeedDriverUsageBaseline(path string) {
	if e == nil {
		return
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return
	}
	e.initializeCostSources(map[string]string{filepath.Clean(path): ""}, false)
}

func (w *pluginUsageWatcher) run(tracker *usageTracker) {
	defer w.entry.endUsageRun()
	defer close(w.doneCh)
	ticker := time.NewTicker(transcriptPollInterval)
	defer ticker.Stop()

	var read os.FileInfo
	for {
		select {
		case <-w.stopCh:
			reconcilePluginUsageOnStop(w.path, read != nil, tracker)
			w.entry.logf("plugin usage watcher: stopped session=%s", w.entry.Label())
			return
		case <-w.entry.registry.host.Done():
			seen := read != nil
			w.entry.deferFinalUsage(func() { reconcilePluginUsageOnStop(w.path, seen, tracker) })
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

func reconcilePluginUsageOnStop(path string, seen bool, tracker *usageTracker) {
	if !seen {
		if _, err := os.Stat(path); err != nil {
			return
		}
	}
	tracker.Reconcile()
}

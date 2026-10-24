package sessionmonitor

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/transcript"
)

type pluginUsageWatcher struct {
	monitor *Monitor
	path    string
	stopCh  chan struct{}
	doneCh  chan struct{}
}

func (m *Monitor) WatchDriverUsage(path string) {
	if m == nil {
		return
	}
	path = strings.TrimSpace(path)
	if m == nil || path == "" {
		return
	}
	m.mu.Lock()
	agent := string(m.agent)
	m.mu.Unlock()
	if !transcript.SupportsUsage(agent) {
		return
	}
	host := m.registry.host
	if !host.Do("seedPluginUsageBaseline", func() { m.SeedDriverUsageBaseline(path) }) {
		return
	}
	m.mu.Lock()
	previous := m.pluginUsageWatch
	if m.usageDraining || previous != nil && previous.path == path {
		m.mu.Unlock()
		return
	}
	watcher := &pluginUsageWatcher{monitor: m, path: path, stopCh: make(chan struct{}), doneCh: make(chan struct{})}
	tracker := newUsageTrackerAt(m, agent, path, transcript.NewReportedUsageSourceResolver(path))
	// Admitted before it replaces the previous watcher, so a refused start leaves that one to stop's final reconcile.
	if !host.Go("runPluginUsageWatcher", func() { watcher.run(tracker) }) {
		m.mu.Unlock()
		return
	}
	m.beginUsageRunLocked()
	m.pluginUsageWatch = watcher
	label := m.label
	m.mu.Unlock()
	if previous != nil {
		close(previous.stopCh)
	}
	m.logf("plugin usage watcher: started session=%s agent=%s path=%s", label, agent, path)
}

func (m *Monitor) SeedDriverUsageBaseline(path string) {
	if m == nil {
		return
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return
	}
	if m.initializeCostSources(map[string]string{filepath.Clean(path): ""}, false) {
		m.publishCost()
	}
}

func (w *pluginUsageWatcher) run(tracker *usageTracker) {
	defer w.monitor.endUsageRun()
	defer close(w.doneCh)
	ticker := time.NewTicker(transcriptPollInterval)
	defer ticker.Stop()

	var read os.FileInfo
	for {
		select {
		case <-w.stopCh:
			reconcilePluginUsageOnStop(w.path, read != nil, tracker)
			w.monitor.logf("plugin usage watcher: stopped session=%s", w.monitor.Label())
			return
		case <-w.monitor.registry.host.Done():
			seen := read != nil
			w.monitor.deferFinalUsage(func() { reconcilePluginUsageOnStop(w.path, seen, tracker) })
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

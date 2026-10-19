package sessionmonitor

import (
	"maps"
	"time"
)

func (e *Entry) ReportHarnessTitle(title string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	e.harnessTitle = title
	view := e.viewLocked()
	e.mu.Unlock()
	return e.home.Applied(view)
}

func (e *Entry) ReportPullRequest(id string, link PullRequest) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	e.pullRequests = maps.Clone(e.pullRequests)
	if e.pullRequests == nil {
		e.pullRequests = make(map[string]PullRequest)
	}
	link.Sequence = e.revision + 1
	e.pullRequests[id] = link
	view := e.viewLocked()
	e.mu.Unlock()
	return e.home.Applied(view)
}

func (e *Entry) ReportSentFiles(paths []string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	e.sentFiles = maps.Clone(e.sentFiles)
	if e.sentFiles == nil {
		e.sentFiles = make(map[string]uint64)
	}
	for _, path := range paths {
		e.sentFiles[path] = e.revision + 1
	}
	view := e.viewLocked()
	e.mu.Unlock()
	return e.home.Applied(view)
}

func (e *Entry) ReofferExit(lifecycle string) (offered, applied bool) {
	if e == nil {
		return false, false
	}
	e.mu.Lock()
	if e.exit == nil || lifecycle == "" || e.exit.LifecycleID != lifecycle {
		e.mu.Unlock()
		return false, false
	}
	view := e.viewLocked()
	e.mu.Unlock()
	return true, e.home.Applied(view)
}

func (e *Entry) ReportExit(exit Exit) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	e.exit = &exit
	view := e.viewLocked()
	e.mu.Unlock()
	return e.home.Applied(view)
}

func (e *Entry) ClearExitBefore(before time.Time) (cleared, applied bool) {
	if e == nil {
		return false, false
	}
	e.mu.Lock()
	if e.exit == nil || !e.exit.At.Before(before) {
		e.mu.Unlock()
		return false, false
	}
	e.exit = nil
	view := e.viewLocked()
	e.mu.Unlock()
	return true, e.home.Applied(view)
}

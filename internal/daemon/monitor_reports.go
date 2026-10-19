package daemon

import "maps"

func (e *monitorEntry) reportHarnessTitle(title string) bool {
	e.mu.Lock()
	e.harnessTitle = title
	view := e.viewLocked()
	e.mu.Unlock()
	return e.sink.Applied(view)
}

func (e *monitorEntry) reportPullRequest(id string, link monitorPullRequest) bool {
	e.mu.Lock()
	e.pullRequests = maps.Clone(e.pullRequests)
	if e.pullRequests == nil {
		e.pullRequests = make(map[string]monitorPullRequest)
	}
	link.Sequence = e.revision + 1
	e.pullRequests[id] = link
	view := e.viewLocked()
	e.mu.Unlock()
	return e.sink.Applied(view)
}

func (e *monitorEntry) reportSentFiles(paths []string) bool {
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
	return e.sink.Applied(view)
}

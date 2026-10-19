package sessionmonitor

import "context"

func (e *Entry) CheckBranch() {
	if e == nil {
		return
	}
	e.mu.Lock()
	directory := e.directory
	e.mu.Unlock()
	if directory == "" {
		return
	}
	branch, err := e.registry.host.ReadBranch(context.Background(), directory)
	if err != nil {
		return
	}
	e.mu.Lock()
	if e.branch == *branch {
		e.mu.Unlock()
		return
	}
	e.branch = *branch
	view := e.viewLocked()
	e.mu.Unlock()
	e.home.Applied(view)
}

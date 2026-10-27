package sessionmonitor

import "context"

func (m *Monitor) CheckBranch() {
	if m == nil {
		return
	}
	m.mu.Lock()
	directory := m.directory
	m.mu.Unlock()
	if directory == "" {
		return
	}
	branch, err := m.registry.host.ReadBranch(context.Background(), directory)
	if err != nil {
		return
	}
	m.mu.Lock()
	if m.branch == *branch {
		m.mu.Unlock()
		return
	}
	m.branch = *branch
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

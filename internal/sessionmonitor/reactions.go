package sessionmonitor

func (m *Monitor) react(view View) {
	m.mu.Lock()
	previous := m.reacted
	if previous.Revision.V >= view.Revision.V {
		m.mu.Unlock()
		return
	}
	m.reacted = view
	m.mu.Unlock()
	state := view.State.V
	if state.Sequence != previous.State.V.Sequence && !state.Quiet && state.Resolve {
		m.ResolveSoon()
	}
}

package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

type Saver interface {
	SaveRuntime(session protocol.SessionID, saved, view View) error
}

type savedView struct {
	view       View
	retry      *time.Timer
	retryDelay time.Duration
}

func (m *Monitor) Session() protocol.SessionID {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.session
}

func (m *Monitor) publish(view View) bool {
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	if stopped || !m.save(view) {
		return false
	}
	return m.home.Applied(view)
}

func (m *Monitor) save(view View) bool {
	saver := m.registry.saver
	m.mu.Lock()
	session := m.session
	m.mu.Unlock()
	if saver == nil || session == "" {
		return true
	}
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	m.mu.Lock()
	saved, stopped := m.saved.view, m.stopped
	m.mu.Unlock()
	if stopped {
		return false
	}
	if saved.Revision.V >= view.Revision.V {
		return true
	}
	err := saver.SaveRuntime(session, saved, view)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.logf("monitor save failed: session=%s: %v", session, err)
		m.retrySaveLocked()
		return false
	}
	m.saved.view = view
	m.stopSaveRetryLocked()
	return true
}

func (m *Monitor) retrySaveLocked() {
	host := m.registry.host
	if m.saved.retry != nil || m.stopped || host.Ended() {
		return
	}
	if m.saved.retryDelay == 0 {
		m.saved.retryDelay = jobs.DefaultBackoffBase
	} else {
		m.saved.retryDelay = min(m.saved.retryDelay*2, jobs.DefaultBackoffCap)
	}
	var timer *time.Timer
	timer = host.AfterFunc("monitorSaveRetry", m.saved.retryDelay, func() {
		m.mu.Lock()
		if m.saved.retry != timer {
			m.mu.Unlock()
			return
		}
		m.saved.retry = nil
		m.mu.Unlock()
		m.Refresh()
	})
	m.saved.retry = timer
}

func (m *Monitor) stopSaveRetryLocked() {
	if m.saved.retry != nil {
		m.saved.retry.Stop()
		m.saved.retry = nil
	}
	m.saved.retryDelay = 0
}

func (m *Monitor) Refresh() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

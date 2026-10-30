package sessionmonitor

import (
	"maps"
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
)

type CostSave struct {
	Checkpoint sessioncost.Checkpoint
	Durable    bool
}

type Saver interface {
	SaveRuntime(session protocol.SessionID, saved, view View, cost *CostSave) error
	MovePending(from, to protocol.SessionID, remaining, moved []Conversation) error
}

type savedView struct {
	view       View
	cost       sessioncost.Checkpoint
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
	if m.Session() == "" {
		if !view.Turn.V.OpenedAt.IsZero() {
			pausepoint.At(pausepoint.ConversationCandidateTurn)
		}
		m.react(view)
		return true
	}
	if !m.savePending() {
		return false
	}
	m.mu.Lock()
	if view.PendingConversation.V != m.pendingHeadLocked() {
		m.mu.Unlock()
		return true
	}
	if m.conversationApplying {
		m.mu.Unlock()
		return true
	}
	pending := view.PendingConversation.V.NativeID != ""
	m.conversationApplying = pending
	m.mu.Unlock()
	if pending {
		if m.registry.host.Go("applyMonitorConversation", func() { m.applyHome(view, true) }) {
			return true
		}
		m.mu.Lock()
		m.conversationApplying = false
		m.mu.Unlock()
		return false
	}
	return m.applyHome(view, pending)
}

func (m *Monitor) applyHome(view View, pending bool) bool {
	outcome, applied := m.home.Applied(view)
	m.react(view)
	if applied && outcome.Conversation != "" {
		m.registry.host.ConversationApplied(m, outcome)
	}
	if pending {
		m.mu.Lock()
		m.conversationApplying = false
		next := m.pendingHeadLocked()
		newer := m.revision > view.Revision.V
		if !applied {
			m.retrySaveLocked()
		}
		m.mu.Unlock()
		if applied && (newer || next != view.PendingConversation.V) {
			m.Refresh()
		}
	}
	return applied
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
	checkpoint := sessioncost.Checkpoint{Initialized: m.costInitialized, Sources: m.cost}
	var cost *CostSave
	if m.saved.cost.Initialized != checkpoint.Initialized || !maps.Equal(m.saved.cost.Sources, checkpoint.Sources) {
		durable := m.saved.cost.Initialized != checkpoint.Initialized || len(m.saved.cost.Sources) != len(checkpoint.Sources)
		for id, source := range checkpoint.Sources {
			prior := m.saved.cost.Sources[id]
			durable = durable || prior == nil || prior.Initialized != source.Initialized || prior.UsageUnavailable != source.UsageUnavailable || prior.MeasurementIncomplete != source.MeasurementIncomplete
		}
		cost = &CostSave{Checkpoint: checkpoint, Durable: durable}
	}
	m.mu.Unlock()
	if stopped {
		return false
	}
	if saved.Revision.V >= view.Revision.V {
		return true
	}
	err := saver.SaveRuntime(session, saved, view, cost)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.logf("monitor save failed: session=%s: %v", session, err)
		m.retrySaveLocked()
		return false
	}
	m.saved.view = view
	m.saved.cost = checkpoint
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

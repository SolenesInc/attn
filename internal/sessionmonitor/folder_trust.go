package sessionmonitor

import (
	"context"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/stepclock"
)

const (
	folderTrustFirstRead  = 10 * time.Second
	folderTrustSecondRead = 90 * time.Second
	folderTrustReadBudget = 5 * time.Second
	folderTrustReadTimer  = stepclock.Name("folder-trust-read")
)

func (m *Monitor) watchFolderTrustQuestionLocked(agent string) {
	if _, reads := agentdriver.Get(agent).(agentdriver.FolderTrustQuestionReader); !reads {
		return
	}
	m.stopFolderTrustReadLocked()
	m.armFolderTrustReadLocked(folderTrustFirstRead, func() {
		m.readFolderTrustQuestion(agent, folderTrustSecondRead-folderTrustFirstRead)
	})
}

func (m *Monitor) armFolderTrustReadLocked(delay time.Duration, read func()) {
	host := m.registry.host
	m.folderTrustRead = host.Clock().AfterFunc(folderTrustReadTimer, delay, func() { host.Do(string(folderTrustReadTimer), read) })
}

func (m *Monitor) stopFolderTrustReadLocked() {
	if m.folderTrustRead != nil {
		m.folderTrustRead.Stop()
		m.folderTrustRead = nil
	}
}

func (m *Monitor) readFolderTrustQuestion(agent string, nextReadAfter time.Duration) {
	m.mu.Lock()
	if !m.awaitingHarness || m.stopped {
		m.mu.Unlock()
		return
	}
	terminal := m.watch.terminalID
	m.folderTrustRead = nil
	if nextReadAfter > 0 {
		m.armFolderTrustReadLocked(nextReadAfter, func() { m.readFolderTrustQuestion(agent, 0) })
	}
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(m.registry.host.Context(), folderTrustReadBudget)
	defer cancel()
	screen, err := m.registry.host.ReadScreen(ctx, terminal)
	if err != nil {
		m.registry.host.Logf("folder trust read failed: session=%s err=%v", m.session, err)
		return
	}
	if !agentdriver.ShowsFolderTrustQuestion(agent, screen) {
		return
	}
	at := time.Now()
	m.UpdateEvidence(func(e *Evidence) bool {
		return !m.awaitingHarness || showsFolderTrust(e)
	}, movedAt(at, func(e *Evidence) {
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      sessionstate.ClaimFolderTrust,
			ObservedAt: at,
		}
	}))
}

func (m *Monitor) clearFolderTrustQuestion() {
	m.UpdateEvidence(func(e *Evidence) bool { return !showsFolderTrust(e) }, func(e *Evidence) { e.LastHarnessEvent = nil })
}

func showsFolderTrust(e *Evidence) bool {
	return e.LastHarnessEvent != nil && e.LastHarnessEvent.Claim == sessionstate.ClaimFolderTrust
}

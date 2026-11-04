package daemon

import (
	"context"
	"fmt"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

type claudeModLink struct {
	mod      *claudeMod
	terminal harness.TerminalID
	session  protocol.SessionID
}

func (claudeModLink) Voices() []harness.Voice {
	return []harness.Voice{harness.VoiceUser, harness.VoiceAttn}
}

func (l claudeModLink) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	delivery := &claudeModDelivery{session: l.session, input: in, answer: make(chan harness.Custody, 1)}
	if !l.mod.enqueue(l.terminal, delivery) {
		return harness.Custody{Reason: fmt.Sprintf("claude mod: terminal %s stopped polling before taking input %s, send it again", l.terminal, in.ID)}
	}
	tripwire := l.mod.d.clock.NewTimer(ClaudeModAckTimer, claudeModAckWait)
	defer tripwire.Stop()
	select {
	case custody := <-delivery.answer:
		return custody
	case <-tripwire.C():
		l.mod.forget(l.terminal, delivery)
		return harness.Custody{Reason: fmt.Sprintf("claude mod: max_ack_wait=%s; no answer for input %s", claudeModAckWait, in.ID)}
	case <-ctx.Done():
		l.mod.forget(l.terminal, delivery)
		return harness.Custody{Reason: fmt.Sprintf("claude mod: input %s: %v", in.ID, ctx.Err())}
	}
}

func (m *claudeMod) enqueue(terminal harness.TerminalID, delivery *claudeModDelivery) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.terminals[terminal]
	if t == nil {
		return false
	}
	t.queued = append(t.queued, delivery)
	m.giveLocked(t)
	return true
}

func (m *claudeMod) forget(terminal harness.TerminalID, delivery *claudeModDelivery) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.terminals[terminal]
	if t == nil {
		return
	}
	delete(t.handed, delivery.input.ID)
	for i, queued := range t.queued {
		if queued == delivery {
			t.queued = append(t.queued[:i], t.queued[i+1:]...)
			return
		}
	}
}

func (m *claudeMod) placeReturned(session protocol.SessionID) {
	m.mu.Lock()
	pending := len(m.returned[session]) > 0
	m.mu.Unlock()
	if pending {
		m.d.life.Go("claudeModPlaceReturned", func() { m.placeReturnedNow(session) })
	}
}

func (m *claudeMod) placeReturnedNow(session protocol.SessionID) {
	for {
		m.mu.Lock()
		queue := m.returned[session]
		m.mu.Unlock()
		if len(queue) == 0 {
			return
		}
		head := queue[0]
		if m.d.store.Get(session) == nil {
			m.mu.Lock()
			delete(m.returned, session)
			m.mu.Unlock()
			return
		}
		attempt := m.d.placeDelivery(context.Background(), session, head)
		if attempt.Stage == sessionmonitor.Deferred {
			if delay, retry := sessionmonitor.RetryDelay(attempt.Err); retry {
				m.d.life.AfterFunc("claudeModPlaceReturned", delay, func() { m.placeReturnedNow(session) })
			} else {
				m.d.logf("claude mod: returned input %s waits for session %s to change state: %v", head.ID, session, attempt.Err)
			}
			return
		}
		if attempt.Stage == sessionmonitor.Failed {
			m.d.logf("claude mod: returned input %s did not reach session %s: %v", head.ID, session, attempt.Err)
		}
		m.dropReturned(session, head)
	}
}

func (m *claudeMod) dropReturned(session protocol.SessionID, placed sessionmonitor.Delivery) {
	m.mu.Lock()
	defer m.mu.Unlock()
	queue := m.returned[session]
	if len(queue) > 0 && queue[0].ID == placed.ID {
		queue = queue[1:]
	}
	if len(queue) == 0 {
		delete(m.returned, session)
		return
	}
	m.returned[session] = queue
}

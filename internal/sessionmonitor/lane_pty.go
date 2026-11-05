package sessionmonitor

import (
	"bytes"
	"context"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

var SubmitDelay = 150 * time.Millisecond

func (l *lane) ptySafetyLocked(delivery Delivery) error {
	if delivery.RequireEmptyComposer && l.userDraft {
		return ErrComposerDirty
	}
	if delivery.AllowUserComposer {
		return nil
	}
	if remaining := quietRemaining(l.monitor.registry.host.LastKey(l.monitor.primaryTerminalLocked()), QuietWindow); remaining > 0 {
		return &quietError{retryAfter: remaining}
	}
	return nil
}

func (m *Monitor) WriteInput(ctx context.Context, terminal harness.TerminalID, data []byte, source string) error {
	if m == nil {
		return ErrLaneClosed
	}
	if !m.mu.TryLock() {
		pausepoint.At(pausepoint.SessionInputLaneContended)
		m.mu.Lock()
	}
	defer m.mu.Unlock()
	l := m.input
	if l.phase == "" {
		l.phase = m.state.State
	}
	if m.noteUserInputLocked(terminal, source, data) {
		l.noteDraftLocked(data)
		if bytes.ContainsAny(data, "\r\n") {
			m.runSteeredLocked()
			l.userSubmit = true
			m.userInputs++
		}
	}
	return m.registry.host.Input(ctx, terminal, data)
}

func (l *lane) noteDraftLocked(data []byte) {
	if l.phase == protocol.SessionStatePendingApproval {
		return
	}
	switch string(data) {
	case "\r", "\x1b", "\x03":
	default:
		l.userDraft = true
	}
}

func (l *lane) clearUnstartedUserSubmitLocked() {
	if l.run == nil {
		l.userSubmit = false
	}
}

func (l *lane) placePTYLocked(ctx context.Context, delivery Delivery) Attempt {
	m := l.monitor
	if err := l.ptySafetyLocked(delivery); err != nil {
		return Attempt{Stage: Deferred, Err: err}
	}
	l.clearUnstartedUserSubmitLocked()

	input := make([]byte, 0, len(pasteStart)+len(delivery.Text)+len(pasteEnd))
	input = append(input, pasteStart...)
	input = append(input, delivery.Text...)
	input = append(input, pasteEnd...)
	terminal := m.primaryTerminalLocked()
	if err := m.registry.host.Input(ctx, terminal, input); err != nil {
		return Attempt{Stage: Failed, Err: err}
	}
	pausepoint.At(pausepoint.SessionInputPasteGap)
	time.Sleep(SubmitDelay)
	if err := m.registry.host.Input(ctx, terminal, []byte("\r")); err != nil {
		return Attempt{Stage: Failed, Err: err}
	}
	return Attempt{Stage: Placed, At: time.Now()}
}

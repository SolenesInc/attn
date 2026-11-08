package sessionmonitor

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
)

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

var (
	SubmitDelay          = 150 * time.Millisecond
	InputPlacementWindow = 5 * time.Second
)

func (l *lane) ptySafetyLocked(delivery Delivery) error {
	if l.monitor.awaitingHarness {
		return errHarnessStarting
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
	l := m.input
	if l.phase == "" {
		l.phase = m.state.State
	}
	if m.noteUserInputLocked(terminal, source, data) {
		if bytes.ContainsAny(data, "\r\n") {
			m.runSteeredLocked()
			l.userSubmit = true
			m.runHeardUserLocked(time.Now())
		}
	}
	m.mu.Unlock()
	return m.registry.host.Input(ctx, terminal, data)
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
	var err error
	l.unlockedWhilePlacing(func() {
		pausepoint.At(pausepoint.SessionInputPlacing)
		err = m.registry.host.PlaceInput(ctx, terminal, input, InputPlacementWindow)
	})
	if err != nil {
		closeWithdrawnPaste := m.registry.host.Input(ctx, terminal, []byte(pasteEnd))
		return Attempt{Stage: Failed, Err: errors.Join(err, closeWithdrawnPaste)}
	}
	pausepoint.At(pausepoint.SessionInputPasteGap)
	time.Sleep(SubmitDelay)
	if err := m.registry.host.Input(ctx, terminal, []byte("\r")); err != nil {
		return Attempt{Stage: Failed, Err: err}
	}
	return Attempt{Stage: Placed, At: time.Now()}
}

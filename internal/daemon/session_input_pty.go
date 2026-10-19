package daemon

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	sessionInputPasteStart = "\x1b[200~"
	sessionInputPasteEnd   = "\x1b[201~"
)

var sessionInputSubmitDelay = 150 * time.Millisecond

func (m sessionInputModule) ptySafetyLocked(sessionID protocol.SessionID, allowUserComposer bool) error {
	if allowUserComposer {
		return nil
	}
	if remaining := m.daemon.userInputQuietRemaining(sessionID, sessionInputQuietWindow); remaining > 0 {
		return &sessionInputQuietError{retryAfter: remaining}
	}
	return nil
}

func (m sessionInputModule) writePTY(ctx context.Context, terminal harness.TerminalID, sessionID protocol.SessionID, data []byte, source string) error {
	entry := m.daemon.monitorEntry(sessionID)
	if entry == nil {
		return errSessionInputLaneClosed
	}
	if !entry.mu.TryLock() {
		pausepoint.At(pausepoint.SessionInputLaneContended)
		entry.mu.Lock()
	}
	defer entry.mu.Unlock()
	lane := entry.input
	if m.daemon.ptyBackend == nil {
		return errors.New("session has no PTY backend")
	}
	if lane.phase == "" && m.daemon.store != nil {
		if session := m.daemon.store.Get(sessionID); session != nil {
			lane.phase = session.State
		}
	}
	if m.daemon.noteUserInput(lane, terminal, sessionID, source, data) && bytes.ContainsAny(data, "\r\n") {
		lane.userSubmit = true
	}
	return m.daemon.ptyBackend.Input(ctx, terminal, data)
}

func (lane *sessionInputLane) clearUnstartedUserSubmitLocked() {
	if lane.run == nil {
		lane.userSubmit = false
	}
}

func (d *Daemon) writeSessionPTY(sessionID protocol.SessionID, data []byte, source string) error {
	return d.sessionInputs().writePTY(context.Background(), d.primaryTerminal(sessionID), sessionID, data, strings.TrimSpace(source))
}

func (d *Daemon) writeTerminalPTY(ctx context.Context, terminal harness.TerminalID, data []byte, source string) error {
	sessionID, shown := d.shownIn(terminal)
	if !shown {
		if d.ptyBackend == nil {
			return errors.New("session has no PTY backend")
		}
		return d.ptyBackend.Input(ctx, terminal, data)
	}
	return d.sessionInputs().writePTY(ctx, terminal, sessionID, data, strings.TrimSpace(source))
}

func (m sessionInputModule) placePTYLocked(ctx context.Context, lane *sessionInputLane, delivery sessionInputDelivery) sessionInputAttempt {
	if m.daemon.ptyBackend == nil {
		return sessionInputAttempt{stage: sessionInputDeferred, err: errors.New("session has no input route")}
	}
	if err := m.ptySafetyLocked(delivery.sessionID, delivery.allowUserComposer); err != nil {
		m.armRetryLocked(lane, delivery, err)
		return sessionInputAttempt{stage: sessionInputDeferred, err: err}
	}
	lane.clearUnstartedUserSubmitLocked()

	input := make([]byte, 0, len(sessionInputPasteStart)+len(delivery.text)+len(sessionInputPasteEnd))
	input = append(input, sessionInputPasteStart...)
	input = append(input, delivery.text...)
	input = append(input, sessionInputPasteEnd...)
	terminal := m.daemon.primaryTerminal(delivery.sessionID)
	if err := m.daemon.ptyBackend.Input(ctx, terminal, input); err != nil {
		return sessionInputAttempt{stage: sessionInputFailed, err: err}
	}
	pausepoint.At(pausepoint.SessionInputPasteGap)
	time.Sleep(sessionInputSubmitDelay)
	if err := m.daemon.ptyBackend.Input(ctx, terminal, []byte("\r")); err != nil {
		return sessionInputAttempt{stage: sessionInputFailed, err: err}
	}
	return sessionInputAttempt{stage: sessionInputPlaced, at: time.Now()}
}

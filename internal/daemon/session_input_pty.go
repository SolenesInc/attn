package daemon

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	sessionInputPasteStart = "\x1b[200~"
	sessionInputPasteEnd   = "\x1b[201~"
)

var sessionInputSubmitDelay = 150 * time.Millisecond

const sessionInputHeldEnterKey = "\x00held-enter"

func (m *sessionInputModule) holdEnterLocked(lane *sessionInputLane, sessionID string, after time.Duration) {
	lane.heldEnter = true
	m.scheduleLocked(lane, sessionID, sessionInputHeldEnterKey, after, func() { m.pressHeldEnter(sessionID) })
}

func (m *sessionInputModule) dropHeldEnterLocked(lane *sessionInputLane) {
	lane.heldEnter = false
	if entry := lane.retries[sessionInputHeldEnterKey]; entry != nil {
		entry.timer.Stop()
		delete(lane.retries, sessionInputHeldEnterKey)
	}
}

func (m *sessionInputModule) pressHeldEnter(sessionID string) {
	lane := m.lane(sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if !lane.heldEnter || lane.stopped {
		return
	}
	ctx := m.daemon.life.Context()
	if m.promptInTheWayLocked(ctx, sessionID) {
		m.holdEnterLocked(lane, sessionID, sessionInputComposerRetry)
		return
	}
	if err := m.daemon.ptyBackend.Input(ctx, sessionID, []byte("\r")); err != nil {
		m.daemon.logf("session input held Enter failed session=%s: %v", sessionID, err)
		m.holdEnterLocked(lane, sessionID, sessionInputComposerRetry)
		return
	}
	m.dropHeldEnterLocked(lane)
}

func (m *sessionInputModule) promptInTheWayLocked(ctx context.Context, sessionID string) bool {
	if state := m.daemon.store.Get(sessionID); state != nil && state.State == protocol.SessionStatePendingApproval {
		return true
	}
	return m.promptShowingLocked(ctx, sessionID)
}

func (m *sessionInputModule) promptShowingLocked(ctx context.Context, sessionID string) bool {
	_, known, selector := m.daemon.sessionInputScreen(ctx, sessionID)
	return !known || selector
}

func (m *sessionInputModule) ptySafetyLocked(ctx context.Context, sessionID string, lane *sessionInputLane, allowUserComposer bool) error {
	if !allowUserComposer {
		if remaining := m.daemon.userInputQuietRemaining(sessionID, sessionInputQuietWindow); remaining > 0 {
			return &sessionInputQuietError{retryAfter: remaining}
		}
	}
	line, known, selector := m.daemon.sessionInputScreen(ctx, sessionID)
	if !known {
		return errSessionInputScreenUnavailable
	}
	if selector {
		m.daemon.logf("session input held off session=%s: the screen is waiting on a keypress (%q)", sessionID, line)
		return errSessionInputBlockedBySelector
	}
	return nil
}

func (m *sessionInputModule) writePTY(ctx context.Context, sessionID string, data []byte, source string) error {
	lane := m.lane(sessionID)
	if !lane.mu.TryLock() {
		pausepoint.At(pausepoint.SessionInputLaneContended)
		lane.mu.Lock()
	}
	defer lane.mu.Unlock()
	if m.daemon.ptyBackend == nil {
		return errors.New("session has no PTY backend")
	}
	if lane.phase == "" && m.daemon.store != nil {
		if session := m.daemon.store.Get(sessionID); session != nil {
			lane.phase = session.State
		}
	}
	if m.daemon.noteUserInput(sessionID, source, data) {
		if lane.heldEnter && m.promptShowingLocked(ctx, sessionID) {
			return m.daemon.ptyBackend.Input(ctx, sessionID, data)
		}
		m.dropHeldEnterLocked(lane)
		m.releaseComposerLocked(lane, sessionID)
		lane.creditNextRun = false
		if bytes.ContainsAny(data, "\r\n") {
			lane.userSubmit = true
		}
	}
	return m.daemon.ptyBackend.Input(ctx, sessionID, data)
}

func (m *sessionInputModule) clearUnstartedUserSubmitLocked(lane *sessionInputLane, sessionID string) {
	if lane.run != nil || !lane.userSubmit {
		return
	}
	lane.userSubmit = false
	m.daemon.forgetUserInput(sessionID)
}

func (d *Daemon) writeSessionPTY(sessionID string, data []byte, source string) error {
	return d.sessionInputs().writePTY(context.Background(), sessionID, data, strings.TrimSpace(source))
}

// A held Enter still counts as custody: the paste already sits in the harness's composer.
func (m *sessionInputModule) placePTYLocked(ctx context.Context, lane *sessionInputLane, delivery sessionInputDelivery) sessionInputAttempt {
	if m.daemon.ptyBackend == nil {
		return sessionInputAttempt{stage: sessionInputDeferred, err: errors.New("session has no input route")}
	}
	if err := m.ptySafetyLocked(ctx, delivery.sessionID, lane, delivery.allowUserComposer); err != nil {
		m.armRetryLocked(lane, delivery, err)
		return sessionInputAttempt{stage: sessionInputDeferred, err: err}
	}
	m.clearUnstartedUserSubmitLocked(lane, delivery.sessionID)

	input := make([]byte, 0, len(sessionInputPasteStart)+len(delivery.text)+len(sessionInputPasteEnd))
	input = append(input, sessionInputPasteStart...)
	input = append(input, delivery.text...)
	input = append(input, sessionInputPasteEnd...)
	if err := m.daemon.ptyBackend.Input(ctx, delivery.sessionID, input); err != nil {
		return sessionInputAttempt{stage: sessionInputFailed, err: err}
	}
	// A busy harness may hold a turn-boundary paste unsubmitted; a prompt-ready one takes it on Enter.
	lane.occupied = delivery.placement == sessionInputAtTurnBoundary
	m.recordOwedLocked(lane, delivery.sessionID)
	pausepoint.At(pausepoint.SessionInputPasteGap)
	time.Sleep(sessionInputSubmitDelay)
	if m.promptInTheWayLocked(ctx, delivery.sessionID) {
		m.daemon.logf("session input holding Enter session=%s: a prompt appeared after the paste", delivery.sessionID)
		m.holdEnterLocked(lane, delivery.sessionID, sessionInputComposerRetry)
		return sessionInputAttempt{stage: sessionInputPlaced, at: time.Now()}
	}
	if err := m.daemon.ptyBackend.Input(ctx, delivery.sessionID, []byte("\r")); err != nil {
		lane.occupied = true
		m.recordOwedLocked(lane, delivery.sessionID)
		return sessionInputAttempt{stage: sessionInputFailed, err: err}
	}
	return sessionInputAttempt{stage: sessionInputPlaced, at: time.Now()}
}

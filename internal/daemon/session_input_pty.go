package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	if _, blocked := m.promptInTheWayLocked(ctx, sessionID); blocked {
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

func (m *sessionInputModule) promptInTheWayLocked(ctx context.Context, sessionID string) (sessionInputReason, bool) {
	if state := m.daemon.store.Get(sessionID); state != nil && state.State == protocol.SessionStatePendingApproval {
		return sessionInputReasonApproval, true
	}
	_, known, selector := m.daemon.sessionInputScreen(ctx, sessionID)
	switch {
	case !known:
		return sessionInputReasonScreenUnavailable, true
	case selector:
		return sessionInputReasonSelector, true
	}
	return sessionInputReasonNone, false
}

func (m *sessionInputModule) promptShowingLocked(ctx context.Context, sessionID string) bool {
	_, known, selector := m.daemon.sessionInputScreen(ctx, sessionID)
	return !known || selector
}

func (m *sessionInputModule) ptySafetyLocked(ctx context.Context, sessionID string, lane *sessionInputLane, allowUserComposer bool) (sessionInputReason, error) {
	if !allowUserComposer {
		if remaining := m.daemon.userInputQuietRemaining(sessionID, sessionInputQuietWindow); remaining > 0 {
			return sessionInputReasonUserComposerDirty, &sessionInputQuietError{retryAfter: remaining}
		}
	}
	line, known, selector := m.daemon.sessionInputScreen(ctx, sessionID)
	if !known {
		return sessionInputReasonScreenUnavailable, errSessionInputScreenUnavailable
	}
	if selector {
		m.daemon.logf("session input held off session=%s: the screen is waiting on a keypress (%q)", sessionID, line)
		return sessionInputReasonSelector, errSessionInputBlockedBySelector
	}
	return sessionInputReasonNone, nil
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
		lane.userGeneration++
		for _, attempt := range lane.attempts {
			if !attempt.composer || (attempt.stage != sessionInputPlaced && attempt.stage != sessionInputIndeterminate) {
				continue
			}
			attempt.stage = sessionInputIndeterminate
			attempt.composer = false
			select {
			case <-attempt.wait:
			default:
				close(attempt.wait)
			}
		}
		m.recordOwedLocked(lane, sessionID)
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

func (m *sessionInputModule) resubmitPTYLocked(ctx context.Context, lane *sessionInputLane, delivery sessionInputDelivery, existing *sessionInputAttemptState) sessionInputAttempt {
	if lane.userGeneration != existing.userGeneration {
		existing.stage = sessionInputIndeterminate
		return sessionInputAttempt{id: delivery.id, stage: sessionInputIndeterminate, route: existing.route, reason: sessionInputReasonUserComposerDirty, wait: existing.wait, err: errSessionInputComposerDirty}
	}
	state := m.daemon.store.Get(delivery.sessionID)
	if state == nil {
		return sessionInputAttempt{id: delivery.id, stage: sessionInputPlaced, route: existing.route, reason: sessionInputReasonGone, wait: existing.wait, err: fmt.Errorf("session %s is gone", delivery.sessionID)}
	}
	if reason, ok := deliveryAllowedForPhase(delivery.placement, state.State); !ok {
		err := errSessionInputBlockedByApproval
		if reason == sessionInputReasonBusy {
			err = fmt.Errorf("session input requires a prompt-ready session, got %s", state.State)
		}
		return sessionInputAttempt{id: delivery.id, stage: sessionInputPlaced, route: existing.route, reason: reason, wait: existing.wait, err: err}
	}
	if reason, err := m.ptySafetyLocked(ctx, delivery.sessionID, lane, delivery.allowUserComposer); err != nil {
		m.armRetryLocked(lane, delivery, err)
		return sessionInputAttempt{id: delivery.id, stage: sessionInputPlaced, route: existing.route, reason: reason, wait: existing.wait, err: err}
	}
	m.clearUnstartedUserSubmitLocked(lane, delivery.sessionID)
	m.dropHeldEnterLocked(lane)
	existing.stage = sessionInputPlaced
	m.recordOwedLocked(lane, delivery.sessionID)
	if err := m.daemon.ptyBackend.Input(ctx, delivery.sessionID, []byte("\r")); err != nil {
		existing.stage = sessionInputIndeterminate
		return sessionInputAttempt{id: delivery.id, stage: sessionInputIndeterminate, route: existing.route, reason: sessionInputReasonTransport, wait: existing.wait, err: err}
	}
	return attemptFromState(delivery.id, existing)
}

func (m *sessionInputModule) placePTYLocked(ctx context.Context, lane *sessionInputLane, delivery sessionInputDelivery, attempt *sessionInputAttemptState, candidate sessionInputCandidate) sessionInputAttempt {
	key := delivery.id.String()
	if m.daemon.ptyBackend == nil {
		delete(lane.attempts, key)
		return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, reason: sessionInputReasonUnsupported, err: errors.New("session has no input route")}
	}
	if reason, err := m.ptySafetyLocked(ctx, delivery.sessionID, lane, delivery.allowUserComposer); err != nil {
		delete(lane.attempts, key)
		m.armRetryLocked(lane, delivery, err)
		return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, route: sessionInputRoutePTY, reason: reason, err: err}
	}
	m.clearUnstartedUserSubmitLocked(lane, delivery.sessionID)

	lane.pending = append(lane.pending, candidate)
	attempt.route = sessionInputRoutePTY
	attempt.composer = true
	attempt.stage = sessionInputPlaced
	m.recordOwedLocked(lane, delivery.sessionID)
	input := make([]byte, 0, len(sessionInputPasteStart)+len(delivery.text)+len(sessionInputPasteEnd))
	input = append(input, sessionInputPasteStart...)
	input = append(input, delivery.text...)
	input = append(input, sessionInputPasteEnd...)
	if err := m.daemon.ptyBackend.Input(ctx, delivery.sessionID, input); err != nil {
		attempt.stage = sessionInputIndeterminate
		return sessionInputAttempt{id: delivery.id, stage: sessionInputIndeterminate, route: sessionInputRoutePTY, reason: sessionInputReasonTransport, wait: attempt.wait, err: err}
	}
	pausepoint.At(pausepoint.SessionInputPasteGap)
	time.Sleep(sessionInputSubmitDelay)
	if reason, blocked := m.promptInTheWayLocked(ctx, delivery.sessionID); blocked {
		m.daemon.logf("session input holding Enter session=%s: a prompt appeared after the paste", delivery.sessionID)
		m.holdEnterLocked(lane, delivery.sessionID, sessionInputComposerRetry)
		return sessionInputAttempt{id: delivery.id, stage: sessionInputPlaced, route: sessionInputRoutePTY, reason: reason, wait: attempt.wait}
	}
	if err := m.daemon.ptyBackend.Input(ctx, delivery.sessionID, []byte("\r")); err != nil {
		attempt.stage = sessionInputIndeterminate
		return sessionInputAttempt{id: delivery.id, stage: sessionInputIndeterminate, route: sessionInputRoutePTY, reason: sessionInputReasonTransport, wait: attempt.wait, err: err}
	}
	return attemptFromState(delivery.id, attempt)
}

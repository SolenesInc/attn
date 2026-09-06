package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// sessionInput owns safe placement and taken receipts for one live session.

type sessionInputStage uint8

const (
	sessionInputDeferred sessionInputStage = iota
	sessionInputPlaced
	sessionInputTaken
	sessionInputIndeterminate
)

type sessionInputReason uint8

const (
	sessionInputReasonNone sessionInputReason = iota
	sessionInputReasonInitialPrompt
	sessionInputReasonApproval
	sessionInputReasonSelector
	sessionInputReasonUserComposerDirty
	sessionInputReasonBusy
	sessionInputReasonScreenUnavailable
	sessionInputReasonGone
	sessionInputReasonUnsupported
	sessionInputReasonIDConflict
	sessionInputReasonTransport
)

type sessionInputRoute uint8

const (
	sessionInputRouteNone sessionInputRoute = iota
	sessionInputRoutePlugin
	sessionInputRoutePTY
)

type sessionInputPlacement uint8

const (
	sessionInputWhenPromptReady sessionInputPlacement = iota
	sessionInputAtTurnBoundary
)

type sessionInputOriginKind uint8

const (
	sessionInputOriginUnknown sessionInputOriginKind = iota
	sessionInputOriginUserConversation
	sessionInputOriginMaintenance
)

type sessionInputOrigin struct {
	kind   sessionInputOriginKind
	source string
}

func userConversationInput() sessionInputOrigin {
	return sessionInputOrigin{kind: sessionInputOriginUserConversation}
}

func maintenanceInput(source string) sessionInputOrigin {
	return sessionInputOrigin{kind: sessionInputOriginMaintenance, source: strings.TrimSpace(source)}
}

type sessionInputAttemptID struct {
	domain string
	key    string
}

func inputAttemptID(domain, key string) sessionInputAttemptID {
	return sessionInputAttemptID{domain: strings.TrimSpace(domain), key: strings.TrimSpace(key)}
}

func (id sessionInputAttemptID) String() string {
	if id.domain == "" || id.key == "" {
		return ""
	}
	return id.domain + "/" + id.key
}

type sessionInputDelivery struct {
	id                sessionInputAttemptID
	sessionID         string
	text              string
	origin            sessionInputOrigin
	placement         sessionInputPlacement
	allowUserComposer bool
	bypassInitialGate bool
	resend            func()
}

func maintenanceSessionInput(domain, key, sessionID, text string, placement sessionInputPlacement) sessionInputDelivery {
	return sessionInputDelivery{
		id:        inputAttemptID(domain, key),
		sessionID: strings.TrimSpace(sessionID),
		text:      text,
		origin:    maintenanceInput(domain),
		placement: placement,
	}
}

func userConversationSessionInput(key, sessionID, text string, placement sessionInputPlacement) sessionInputDelivery {
	return sessionInputDelivery{
		id:        inputAttemptID("user-conversation", key),
		sessionID: strings.TrimSpace(sessionID),
		text:      text,
		origin:    userConversationInput(),
		placement: placement,
	}
}

func annotationSessionInput(key, sessionID, text string) sessionInputDelivery {
	delivery := userConversationSessionInput(key, sessionID, text, sessionInputAtTurnBoundary)
	delivery.allowUserComposer = true
	return delivery
}

type sessionInputRunRef struct {
	sessionID string
	epoch     uint64
}

func (r sessionInputRunRef) valid() bool { return r.sessionID != "" && r.epoch > 0 }

type sessionInputReceipt struct {
	id      sessionInputAttemptID
	run     sessionInputRunRef
	takenAt time.Time
}

type sessionInputAttempt struct {
	id      sessionInputAttemptID
	stage   sessionInputStage
	route   sessionInputRoute
	reason  sessionInputReason
	receipt *sessionInputReceipt
	wait    <-chan struct{}
	err     error
}

type sessionInputFingerprint struct {
	sessionID         string
	text              string
	origin            sessionInputOrigin
	placement         sessionInputPlacement
	allowUserComposer bool
	bypassInitialGate bool
}

type sessionInputAttemptState struct {
	fingerprint    sessionInputFingerprint
	stage          sessionInputStage
	route          sessionInputRoute
	receipt        *sessionInputReceipt
	wait           chan struct{}
	composer       bool
	userGeneration uint64
	released       bool
}

type sessionInputCandidate struct {
	inputID string
	attempt sessionInputAttemptID
	text    string
	origin  sessionInputOrigin
}

type sessionInputRunState struct {
	ref       sessionInputRunRef
	userTaken bool
}

type sessionInputLane struct {
	mu sync.Mutex

	attempts       map[string]*sessionInputAttemptState
	retries        map[string]*sessionInputRetry
	pending        []sessionInputCandidate
	run            *sessionInputRunState
	placing        bool
	epoch          uint64
	userGeneration uint64
	userSubmit     bool
	phase          protocol.SessionState
	stopped        bool
	running        sync.WaitGroup
}

type sessionInputModule struct {
	daemon  *Daemon
	mu      sync.Mutex
	lanes   map[string]*sessionInputLane
	stopped bool
}

type sessionInputTakenEffect struct {
	inputID string
	origin  sessionInputOrigin
	run     sessionInputRunRef
	at      time.Time
}

type sessionInputEffects struct {
	taken          *sessionInputTakenEffect
	receipt        *sessionInputReceipt
	requestStarted *time.Time
}

var (
	errSessionInputBlockedByApproval = errors.New("session input blocked by pending approval")
	errSessionInputBlockedBySelector = errors.New("session input blocked by an on-screen selector")
	errSessionInputComposerDirty     = errors.New("session input blocked by the user's composer")
	errSessionInputComposerOccupied  = errors.New("session input blocked by an unresolved automated composer")
	errSessionInputPlacingAnother    = errors.New("another session input is being placed")
	errSessionInputLaneClosed        = errors.New("the session's input lane is closed")
	errSessionInputScreenUnavailable = errors.New("session input blocked because the screen is unavailable")
	errSessionInputInitialPrompt     = errors.New("session input blocked while the initial prompt is pending")
)

const (
	sessionInputPasteStart = "\x1b[200~"
	sessionInputPasteEnd   = "\x1b[201~"
)

// Claude Code 2.1.x receipt: 0ms failed, 50ms submitted; 150ms adds load margin.
var sessionInputSubmitDelay = 150 * time.Millisecond

// Claude Code 2.1.228 receipt: warm 181/187ms, fresh 1.06s; 3s is the tripwire.
var sessionInputTakenWindow = 3 * time.Second

// Receipt (daemon.log, 9h, 5,588 keystrokes): p99 gap 3s, p99.5 12s; 30s is the tripwire.
var sessionInputQuietWindow = 30 * time.Second

type sessionInputQuietError struct{ retryAfter time.Duration }

func (e *sessionInputQuietError) Error() string {
	return fmt.Sprintf("%v (retry in %s)", errSessionInputComposerDirty, e.retryAfter)
}

func (e *sessionInputQuietError) Unwrap() error { return errSessionInputComposerDirty }

type sessionInputRetry struct {
	timer  *time.Timer
	resend func()
}

// A collision clears when the placed prompt is taken, so it waits a take's own
// span. Read once: tests move sessionInputTakenWindow.
var sessionInputComposerRetry = sessionInputTakenWindow

func sessionInputRetryDelay(err error) (time.Duration, bool) {
	var quiet *sessionInputQuietError
	if errors.As(err, &quiet) {
		return quiet.retryAfter, true
	}
	if errors.Is(err, errSessionInputComposerOccupied) || errors.Is(err, errSessionInputPlacingAnother) {
		return sessionInputComposerRetry, true
	}
	return 0, false
}

func sessionInputDeferredError(err error) bool {
	return errors.Is(err, errSessionInputBlockedByApproval) ||
		errors.Is(err, errSessionInputBlockedBySelector) ||
		errors.Is(err, errSessionInputComposerDirty) ||
		errors.Is(err, errSessionInputComposerOccupied) ||
		errors.Is(err, errSessionInputScreenUnavailable) ||
		errors.Is(err, errSessionInputInitialPrompt)
}

func (d *Daemon) sessionInputs() *sessionInputModule {
	d.sessionInputOnce.Do(func() {
		d.sessionInputState = &sessionInputModule{daemon: d, lanes: make(map[string]*sessionInputLane)}
	})
	return d.sessionInputState
}

func (m *sessionInputModule) lane(sessionID string) *sessionInputLane {
	m.mu.Lock()
	defer m.mu.Unlock()
	lane := m.lanes[sessionID]
	if lane == nil {
		lane = &sessionInputLane{attempts: make(map[string]*sessionInputAttemptState), stopped: m.stopped}
		m.lanes[sessionID] = lane
	}
	return lane
}

func (m *sessionInputModule) release(sessionID string, id sessionInputAttemptID) {
	key := id.String()
	if key == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	m.mu.Lock()
	lane := m.lanes[sessionID]
	m.mu.Unlock()
	if lane == nil {
		return
	}
	lane.mu.Lock()
	if state := lane.attempts[key]; state != nil {
		if state.stage == sessionInputTaken {
			delete(lane.attempts, key)
		} else {
			state.released = true
		}
	}
	lane.mu.Unlock()
}

func (m *sessionInputModule) forget(sessionID string, id sessionInputAttemptID) {
	key := id.String()
	if key == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	m.mu.Lock()
	lane := m.lanes[sessionID]
	m.mu.Unlock()
	if lane == nil {
		return
	}
	lane.mu.Lock()
	delete(lane.attempts, key)
	lane.removePending(id)
	lane.mu.Unlock()
}

func (m *sessionInputModule) relinquishComposer(sessionID string, id sessionInputAttemptID) {
	key := id.String()
	if key == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	m.mu.Lock()
	lane := m.lanes[sessionID]
	m.mu.Unlock()
	if lane == nil {
		return
	}
	lane.mu.Lock()
	if state := lane.attempts[key]; state != nil && state.stage == sessionInputPlaced {
		state.composer = false
	}
	lane.mu.Unlock()
}

func (m *sessionInputModule) forgetSuperseded(sessionID string, current sessionInputAttemptID, origin sessionInputOrigin) {
	currentKey := current.String()
	if currentKey == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	m.mu.Lock()
	lane := m.lanes[sessionID]
	m.mu.Unlock()
	if lane == nil {
		return
	}
	lane.mu.Lock()
	defer lane.mu.Unlock()
	for key, state := range lane.attempts {
		if key == currentKey || state.fingerprint.origin != origin || state.composer {
			continue
		}
		for i := 0; i < len(lane.pending); {
			if lane.pending[i].attempt.String() == key {
				lane.pending = append(lane.pending[:i], lane.pending[i+1:]...)
				continue
			}
			i++
		}
		select {
		case <-state.wait:
		default:
			close(state.wait)
		}
		delete(lane.attempts, key)
	}
}

// Closed in place before it is dropped: a callback resuming mid-drain finds a
// closed lane, not a fresh one built for the replacement runtime.
func (m *sessionInputModule) forgetSession(sessionID string) {
	lane := m.closeLane(sessionID)
	if lane == nil {
		return
	}
	m.mu.Lock()
	if m.lanes[sessionID] == lane {
		delete(m.lanes, sessionID)
	}
	m.mu.Unlock()
}

func (m *sessionInputModule) fenceSession(sessionID string) {
	m.closeLane(sessionID)
}

// Never call from a resend: it waits for every resend the lane has in flight.
func (m *sessionInputModule) closeLane(sessionID string) *sessionInputLane {
	m.mu.Lock()
	lane := m.lanes[sessionID]
	m.mu.Unlock()
	if lane == nil {
		return nil
	}
	lane.mu.Lock()
	lane.stopRetriesLocked()
	lane.mu.Unlock()
	lane.running.Wait()
	return lane
}

func (m *sessionInputModule) armRetryLocked(lane *sessionInputLane, delivery sessionInputDelivery, err error) {
	after, retryable := sessionInputRetryDelay(err)
	if !retryable || delivery.resend == nil || lane.stopped {
		return
	}
	select {
	case <-m.daemon.done:
		return
	default:
	}
	key := delivery.id.String()
	if lane.retries == nil {
		lane.retries = make(map[string]*sessionInputRetry)
	}
	if existing := lane.retries[key]; existing != nil {
		existing.timer.Stop()
	}
	entry := &sessionInputRetry{resend: delivery.resend}
	sessionID := delivery.sessionID
	entry.timer = time.AfterFunc(after, func() { m.fireRetry(sessionID, key, entry) })
	lane.retries[key] = entry
}

// A stopped timer may already be running its callback: it resends only while it
// still owns its slot on an open lane, and is counted until it returns.
func (m *sessionInputModule) fireRetry(sessionID, key string, self *sessionInputRetry) {
	m.mu.Lock()
	lane := m.lanes[sessionID]
	m.mu.Unlock()
	if lane == nil {
		return
	}
	lane.mu.Lock()
	owner := !lane.stopped && lane.retries[key] == self
	if owner {
		delete(lane.retries, key)
		lane.running.Add(1)
	}
	lane.mu.Unlock()
	if !owner {
		return
	}
	defer lane.running.Done()
	self.resend()
}

// Every live lane is closed under its own lock before the wait, and a lane born
// later is born closed, so no resend can start once this returns.
func (m *sessionInputModule) stopRetries() {
	m.mu.Lock()
	m.stopped = true
	lanes := make([]*sessionInputLane, 0, len(m.lanes))
	for _, lane := range m.lanes {
		lanes = append(lanes, lane)
	}
	m.mu.Unlock()
	for _, lane := range lanes {
		lane.mu.Lock()
		lane.stopRetriesLocked()
		lane.mu.Unlock()
	}
	for _, lane := range lanes {
		lane.running.Wait()
	}
}

func (lane *sessionInputLane) stopRetriesLocked() {
	lane.stopped = true
	for key, entry := range lane.retries {
		entry.timer.Stop()
		delete(lane.retries, key)
	}
}

func (m *sessionInputModule) try(ctx context.Context, delivery sessionInputDelivery) sessionInputAttempt {
	key := delivery.id.String()
	if key == "" || strings.TrimSpace(delivery.sessionID) == "" || strings.TrimSpace(delivery.text) == "" {
		return sessionInputAttempt{id: delivery.id, stage: sessionInputIndeterminate, reason: sessionInputReasonUnsupported, err: errors.New("session input needs an attempt id, session id, and text")}
	}
	lane := m.lane(delivery.sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if lane.stopped {
		return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, reason: sessionInputReasonGone, err: errSessionInputLaneClosed}
	}

	fingerprint := sessionInputFingerprint{
		sessionID:         delivery.sessionID,
		text:              delivery.text,
		origin:            delivery.origin,
		placement:         delivery.placement,
		allowUserComposer: delivery.allowUserComposer,
		bypassInitialGate: delivery.bypassInitialGate,
	}
	if existing := lane.attempts[key]; existing != nil {
		if existing.fingerprint != fingerprint {
			return sessionInputAttempt{id: delivery.id, stage: sessionInputIndeterminate, reason: sessionInputReasonIDConflict, err: fmt.Errorf("session input attempt %s was reused with different content", key)}
		}
		if existing.stage == sessionInputTaken {
			return attemptFromState(delivery.id, existing)
		}
		if (existing.stage == sessionInputPlaced || existing.stage == sessionInputIndeterminate) &&
			existing.route == sessionInputRoutePTY && existing.composer {
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
			if err := m.daemon.ptyBackend.Input(ctx, delivery.sessionID, []byte("\r")); err != nil {
				existing.stage = sessionInputIndeterminate
				return sessionInputAttempt{id: delivery.id, stage: sessionInputIndeterminate, route: existing.route, reason: sessionInputReasonTransport, wait: existing.wait, err: err}
			}
			existing.stage = sessionInputPlaced
		}
		return attemptFromState(delivery.id, existing)
	}
	if lane.placing {
		m.armRetryLocked(lane, delivery, errSessionInputPlacingAnother)
		return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, reason: sessionInputReasonBusy, err: errSessionInputPlacingAnother}
	}
	for _, existing := range lane.attempts {
		if existing.composer && (existing.stage == sessionInputPlaced || existing.stage == sessionInputIndeterminate) {
			m.armRetryLocked(lane, delivery, errSessionInputComposerOccupied)
			return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, reason: sessionInputReasonBusy, err: errSessionInputComposerOccupied}
		}
	}

	state := m.daemon.store.Get(delivery.sessionID)
	if state == nil {
		return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, reason: sessionInputReasonGone, err: fmt.Errorf("session %s is gone", delivery.sessionID)}
	}
	if !delivery.bypassInitialGate && m.daemon.initialPromptPending(delivery.sessionID) {
		return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, reason: sessionInputReasonInitialPrompt, err: errSessionInputInitialPrompt}
	}
	if reason, ok := deliveryAllowedForPhase(delivery.placement, state.State); !ok {
		err := errSessionInputBlockedByApproval
		if reason == sessionInputReasonBusy {
			err = fmt.Errorf("session input requires a prompt-ready session, got %s", state.State)
		}
		return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, reason: reason, err: err}
	}

	attempt := &sessionInputAttemptState{fingerprint: fingerprint, wait: make(chan struct{}), userGeneration: lane.userGeneration}
	lane.attempts[key] = attempt
	inputID := key
	candidate := sessionInputCandidate{inputID: inputID, attempt: delivery.id, text: delivery.text, origin: delivery.origin}

	if m.daemon.sessionUsesPluginMessageDelivery(state) {
		attempt.route = sessionInputRoutePlugin
		lane.pending = append(lane.pending, candidate)
		lane.placing = true
		lane.mu.Unlock()
		delivered, err := m.daemon.deliverSessionInputViaPluginDriver(state, inputID, delivery.text)
		lane.mu.Lock()
		lane.placing = false
		if !delivered {
			lane.removePending(delivery.id)
			attempt.route = sessionInputRouteNone
		} else if err != nil && attempt.stage != sessionInputTaken {
			lane.removePending(delivery.id)
			delete(lane.attempts, key)
			return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, route: sessionInputRoutePlugin, reason: sessionInputReasonTransport, err: err}
		} else {
			if attempt.stage != sessionInputTaken {
				attempt.stage = sessionInputPlaced
			}
			go m.daemon.maybeGenerateSessionTitleFromPrompt(delivery.sessionID, delivery.text, delivery.origin)
			return attemptFromState(delivery.id, attempt)
		}
	}

	if m.daemon.ptyBackend == nil {
		delete(lane.attempts, key)
		return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, reason: sessionInputReasonUnsupported, err: errors.New("session has no input route")}
	}
	if reason, err := m.ptySafetyLocked(ctx, delivery.sessionID, lane, delivery.allowUserComposer); err != nil {
		delete(lane.attempts, key)
		m.armRetryLocked(lane, delivery, err)
		return sessionInputAttempt{id: delivery.id, stage: sessionInputDeferred, route: sessionInputRoutePTY, reason: reason, err: err}
	}

	lane.pending = append(lane.pending, candidate)
	attempt.route = sessionInputRoutePTY
	attempt.composer = true
	input := make([]byte, 0, len(sessionInputPasteStart)+len(delivery.text)+len(sessionInputPasteEnd))
	input = append(input, sessionInputPasteStart...)
	input = append(input, delivery.text...)
	input = append(input, sessionInputPasteEnd...)
	if err := m.daemon.ptyBackend.Input(ctx, delivery.sessionID, input); err != nil {
		attempt.stage = sessionInputIndeterminate
		return sessionInputAttempt{id: delivery.id, stage: sessionInputIndeterminate, route: sessionInputRoutePTY, reason: sessionInputReasonTransport, wait: attempt.wait, err: err}
	}
	time.Sleep(sessionInputSubmitDelay)
	if err := m.daemon.ptyBackend.Input(ctx, delivery.sessionID, []byte("\r")); err != nil {
		attempt.stage = sessionInputIndeterminate
		return sessionInputAttempt{id: delivery.id, stage: sessionInputIndeterminate, route: sessionInputRoutePTY, reason: sessionInputReasonTransport, wait: attempt.wait, err: err}
	}
	attempt.stage = sessionInputPlaced
	return attemptFromState(delivery.id, attempt)
}

func (lane *sessionInputLane) removePending(id sessionInputAttemptID) {
	for i := 0; i < len(lane.pending); {
		if lane.pending[i].attempt == id {
			lane.pending = append(lane.pending[:i], lane.pending[i+1:]...)
			continue
		}
		i++
	}
}

func attemptFromState(id sessionInputAttemptID, state *sessionInputAttemptState) sessionInputAttempt {
	return sessionInputAttempt{id: id, stage: state.stage, route: state.route, receipt: state.receipt, wait: state.wait}
}

func deliveryAllowedForPhase(placement sessionInputPlacement, state protocol.SessionState) (sessionInputReason, bool) {
	if state == protocol.SessionStatePendingApproval {
		return sessionInputReasonApproval, false
	}
	switch placement {
	case sessionInputWhenPromptReady:
		if state == protocol.SessionStateIdle || state == protocol.SessionStateWaitingInput {
			return sessionInputReasonNone, true
		}
		return sessionInputReasonBusy, false
	case sessionInputAtTurnBoundary:
		return sessionInputReasonNone, true
	default:
		return sessionInputReasonUnsupported, false
	}
}

func sessionInputPhaseAllows(placement sessionInputPlacement, state protocol.SessionState) bool {
	_, ok := deliveryAllowedForPhase(placement, state)
	return ok
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
	lane.mu.Lock()
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
		if bytes.ContainsAny(data, "\r\n") {
			lane.userSubmit = true
		}
	}
	return m.daemon.ptyBackend.Input(ctx, sessionID, data)
}

func (m *sessionInputModule) observePromptTaken(sessionID, prompt string, at time.Time) sessionInputEffects {
	if at.IsZero() {
		at = time.Now()
	}
	lane := m.lane(sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()

	var candidate *sessionInputCandidate
	matchIndexes := make([]int, 0, 1)
	for i := range lane.pending {
		if lane.pending[i].text == prompt {
			matchIndexes = append(matchIndexes, i)
		}
	}
	plausible := len(matchIndexes)
	if lane.userSubmit {
		plausible++
	}
	if plausible == 1 && len(matchIndexes) == 1 {
		i := matchIndexes[0]
		copy := lane.pending[i]
		candidate = &copy
		lane.pending = append(lane.pending[:i], lane.pending[i+1:]...)
	} else if plausible == 1 {
		candidate = &sessionInputCandidate{
			inputID: fmt.Sprintf("user/%d", lane.userGeneration),
			text:    prompt,
			origin:  userConversationInput(),
		}
	} else if plausible > 1 {
		for i := len(matchIndexes) - 1; i >= 0; i-- {
			index := matchIndexes[i]
			matched := lane.pending[index]
			if state := lane.attempts[matched.attempt.String()]; state != nil {
				state.stage = sessionInputIndeterminate
				state.composer = false
				select {
				case <-state.wait:
				default:
					close(state.wait)
				}
				if state.released {
					delete(lane.attempts, matched.attempt.String())
				}
			}
			lane.pending = append(lane.pending[:index], lane.pending[index+1:]...)
		}
	}
	if lane.userSubmit {
		kept := lane.pending[:0]
		for _, pending := range lane.pending {
			state := lane.attempts[pending.attempt.String()]
			if state == nil || state.stage != sessionInputIndeterminate {
				kept = append(kept, pending)
			}
		}
		lane.pending = kept
	}
	m.daemon.forgetUserInput(sessionID)
	lane.userSubmit = false
	return m.takeLocked(lane, sessionID, candidate, at)
}

func (m *sessionInputModule) observeInputTaken(sessionID, inputID string, at time.Time) sessionInputEffects {
	if at.IsZero() {
		at = time.Now()
	}
	lane := m.lane(sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	var candidate *sessionInputCandidate
	for i := range lane.pending {
		if lane.pending[i].inputID == inputID {
			copy := lane.pending[i]
			candidate = &copy
			lane.pending = append(lane.pending[:i], lane.pending[i+1:]...)
			break
		}
	}
	if candidate == nil {
		return sessionInputEffects{}
	}
	return m.takeLocked(lane, sessionID, candidate, at)
}

func (m *sessionInputModule) takeLocked(lane *sessionInputLane, sessionID string, candidate *sessionInputCandidate, at time.Time) sessionInputEffects {
	if laneRun := m.ensureRunLocked(lane, sessionID); laneRun != nil {
		effects := sessionInputEffects{requestStarted: &at}
		if candidate == nil {
			return effects
		}
		taken := &sessionInputTakenEffect{inputID: candidate.inputID, origin: candidate.origin, run: laneRun.ref, at: at}
		if candidate.origin.kind == sessionInputOriginUserConversation {
			laneRun.userTaken = true
		}
		effects.taken = taken
		if state := lane.attempts[candidate.attempt.String()]; state != nil {
			receipt := &sessionInputReceipt{id: candidate.attempt, run: laneRun.ref, takenAt: at}
			state.stage = sessionInputTaken
			state.receipt = receipt
			state.composer = false
			select {
			case <-state.wait:
			default:
				close(state.wait)
			}
			effects.receipt = receipt
			if state.released {
				delete(lane.attempts, candidate.attempt.String())
			}
		}
		return effects
	}
	return sessionInputEffects{requestStarted: &at}
}

func (m *sessionInputModule) ensureRunLocked(lane *sessionInputLane, sessionID string) *sessionInputRunState {
	if lane.run != nil {
		return lane.run
	}
	lane.epoch++
	lane.run = &sessionInputRunState{ref: sessionInputRunRef{sessionID: sessionID, epoch: lane.epoch}}
	return lane.run
}

func (m *sessionInputModule) observePhase(sessionID string, phase protocol.SessionState) {
	lane := m.lane(sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	previous := lane.phase
	lane.phase = phase
	if phase == protocol.SessionStateWorking {
		m.ensureRunLocked(lane, sessionID)
		if lane.userSubmit || previous == protocol.SessionStatePendingApproval {
			lane.clearConsumedUserInputLocked()
			m.daemon.forgetUserInput(sessionID)
		}
		return
	}
	lane.run = nil
}

func (lane *sessionInputLane) clearConsumedUserInputLocked() {
	kept := lane.pending[:0]
	for _, pending := range lane.pending {
		state := lane.attempts[pending.attempt.String()]
		if state == nil || state.stage != sessionInputIndeterminate {
			kept = append(kept, pending)
		} else if state.released {
			delete(lane.attempts, pending.attempt.String())
		}
	}
	lane.pending = kept
	lane.userSubmit = false
}

func (m *sessionInputModule) currentUserRun(sessionID string) (sessionInputRunRef, bool) {
	lane := m.lane(sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if lane.run == nil || !lane.run.userTaken {
		return sessionInputRunRef{}, false
	}
	return lane.run.ref, true
}

func (m *sessionInputModule) await(sessionID string, id sessionInputAttemptID, wait <-chan struct{}, window time.Duration) sessionInputAttempt {
	if wait == nil || window <= 0 {
		return m.lookup(sessionID, id)
	}
	timer := time.NewTimer(window)
	defer timer.Stop()
	select {
	case <-wait:
	case <-timer.C:
	}
	return m.lookup(sessionID, id)
}

func (m *sessionInputModule) lookup(sessionID string, id sessionInputAttemptID) sessionInputAttempt {
	key := id.String()
	m.mu.Lock()
	lane := m.lanes[sessionID]
	m.mu.Unlock()
	if lane != nil {
		lane.mu.Lock()
		if state := lane.attempts[key]; state != nil {
			attempt := attemptFromState(id, state)
			lane.mu.Unlock()
			return attempt
		}
		lane.mu.Unlock()
	}
	return sessionInputAttempt{id: id, stage: sessionInputDeferred, reason: sessionInputReasonGone}
}

func (d *Daemon) observePromptTaken(sessionID, prompt string, at time.Time) sessionInputEffects {
	effects := d.sessionInputs().observePromptTaken(sessionID, prompt, at)
	if effects.requestStarted != nil {
		d.markModelRequestStarted(sessionID, *effects.requestStarted)
	}
	if effects.taken != nil && effects.taken.origin.kind == sessionInputOriginUserConversation {
		d.armAutoSettleForUserInput(effects.taken.run)
	}
	return effects
}

func (d *Daemon) observeStructuredInputTaken(sessionID, inputID string, at time.Time) sessionInputEffects {
	effects := d.sessionInputs().observeInputTaken(sessionID, inputID, at)
	if effects.requestStarted != nil {
		d.markModelRequestStarted(sessionID, *effects.requestStarted)
	}
	if effects.taken != nil && effects.taken.origin.kind == sessionInputOriginUserConversation {
		d.armAutoSettleForUserInput(effects.taken.run)
	}
	return effects
}

func (d *Daemon) markModelRequestStarted(sessionID string, at time.Time) bool {
	if d.store == nil || !d.store.MarkModelRequestStarted(sessionID, at) {
		return false
	}
	d.publishFact(FactSessionModelRequestStarted, sessionID, nil)
	return true
}

func (d *Daemon) writeSessionPTY(sessionID string, data []byte, source string) error {
	return d.sessionInputs().writePTY(context.Background(), sessionID, data, strings.TrimSpace(source))
}

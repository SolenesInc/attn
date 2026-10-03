package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

type sessionInputStage uint8

const (
	sessionInputDeferred sessionInputStage = iota
	sessionInputPlaced
	sessionInputFailed
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

func (o sessionInputOrigin) voice() harness.Voice {
	if o.kind == sessionInputOriginMaintenance {
		return harness.VoiceAttn
	}
	return harness.VoiceUser
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

// Custody is a delivery's only result: never confirm afterwards that the harness used it.
type sessionInputAttempt struct {
	stage sessionInputStage
	at    time.Time
	err   error
}

type sessionInputRunState struct {
	ref      sessionInputRunRef
	credited bool
}

type sessionInputLane struct {
	mu sync.Mutex

	retries map[string]*sessionInputRetry
	run     *sessionInputRunState
	placing bool
	// attn's paste may still sit in the composer. Only a turn start or the
	// user's own typing clears it: nothing confirms the harness took the paste.
	occupied      bool
	creditNextRun bool
	titleNext     *sessionInputDelivery
	epoch         uint64
	userSubmit    bool
	heldEnter     bool
	phase         protocol.SessionState
	stopped       bool
	running       sync.WaitGroup
}

type sessionInputModule struct {
	daemon  *Daemon
	mu      sync.Mutex
	lanes   map[string]*sessionInputLane
	stopped bool
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

var sessionInputComposerRetry = 3 * time.Second

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
		lane = &sessionInputLane{stopped: m.stopped}
		m.lanes[sessionID] = lane
	}
	return lane
}

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
	if !retryable || delivery.resend == nil {
		return
	}
	m.scheduleLocked(lane, delivery.sessionID, delivery.id.String(), after, delivery.resend)
}

func (m *sessionInputModule) scheduleLocked(lane *sessionInputLane, sessionID, key string, after time.Duration, resend func()) {
	if lane.stopped {
		return
	}
	if m.daemon.life.Ended() {
		return
	}
	if lane.retries == nil {
		lane.retries = make(map[string]*sessionInputRetry)
	}
	if existing := lane.retries[key]; existing != nil {
		existing.timer.Stop()
	}
	entry := &sessionInputRetry{resend: resend}
	entry.timer = m.daemon.life.AfterFunc("sessionInputRetry", after, func() { m.fireRetry(sessionID, key, entry) })
	lane.retries[key] = entry
}

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
	if delivery.id.String() == "" || strings.TrimSpace(delivery.sessionID) == "" || strings.TrimSpace(delivery.text) == "" {
		return sessionInputAttempt{stage: sessionInputFailed, err: errors.New("session input needs an attempt id, session id, and text")}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(m.daemon.life.Context(), cancel)()
	attempt, credited := m.place(ctx, delivery)
	if credited.valid() {
		m.daemon.armAutoSettleForUserInput(credited)
	}
	return attempt
}

func (m *sessionInputModule) place(ctx context.Context, delivery sessionInputDelivery) (sessionInputAttempt, sessionInputRunRef) {
	lane := m.lane(delivery.sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	deferred := func(err error) (sessionInputAttempt, sessionInputRunRef) {
		return sessionInputAttempt{stage: sessionInputDeferred, err: err}, sessionInputRunRef{}
	}
	if lane.stopped {
		return deferred(errSessionInputLaneClosed)
	}
	if lane.placing {
		m.armRetryLocked(lane, delivery, errSessionInputPlacingAnother)
		return deferred(errSessionInputPlacingAnother)
	}
	if lane.heldEnter || lane.occupied {
		m.armRetryLocked(lane, delivery, errSessionInputComposerOccupied)
		return deferred(errSessionInputComposerOccupied)
	}

	state := m.daemon.store.Get(delivery.sessionID)
	if state == nil {
		return deferred(fmt.Errorf("session %s is gone", delivery.sessionID))
	}
	if !delivery.bypassInitialGate && m.daemon.initialPromptPending(delivery.sessionID) {
		return deferred(errSessionInputInitialPrompt)
	}
	if err := deliveryAllowedForPhase(delivery.placement, state.State); err != nil {
		return deferred(err)
	}

	var attempt sessionInputAttempt
	handled := false
	if m.daemon.sessionLink(state, delivery.origin.voice()) != nil {
		attempt, handled = m.placeOverLinkLocked(ctx, lane, delivery, state)
	}
	if !handled {
		attempt = m.placePTYLocked(ctx, lane, delivery)
	}
	if attempt.stage != sessionInputPlaced {
		return attempt, sessionInputRunRef{}
	}
	return attempt, m.tookLocked(lane, delivery)
}

func (m *sessionInputModule) placeOverLinkLocked(ctx context.Context, lane *sessionInputLane, delivery sessionInputDelivery, state *protocol.Session) (sessionInputAttempt, bool) {
	voice := delivery.origin.voice()
	lane.placing = true
	lane.mu.Unlock()
	// Resolve again after unlocking: a link gone meanwhile hands the input to the PTY.
	link := m.daemon.sessionLink(state, voice)
	var custody harness.Custody
	if link != nil {
		custody = link.Deliver(ctx, harness.Input{Session: state.ID, ID: delivery.id.String(), Text: delivery.text, Voice: voice})
	}
	lane.mu.Lock()
	lane.placing = false
	if link == nil {
		return sessionInputAttempt{}, false
	}
	if !custody.Taken {
		return sessionInputAttempt{stage: sessionInputDeferred, err: errors.New(custody.Reason)}, true
	}
	return sessionInputAttempt{stage: sessionInputPlaced, at: custody.At}, true
}

// tookLocked credits the user's words to the run going, or, while the paste waits in the
// composer, to the next turn start. The user typing over the paste forfeits that credit.
func (m *sessionInputModule) tookLocked(lane *sessionInputLane, delivery sessionInputDelivery) sessionInputRunRef {
	if delivery.origin.kind != sessionInputOriginUserConversation {
		return sessionInputRunRef{}
	}
	if lane.run == nil || lane.occupied {
		lane.creditNextRun = true
		if lane.occupied || lane.heldEnter {
			lane.titleNext = &delivery
		} else {
			m.titleLocked(delivery)
		}
		return sessionInputRunRef{}
	}
	m.titleLocked(delivery)
	lane.run.credited = true
	return lane.run.ref
}

func (m *sessionInputModule) titleLocked(delivery sessionInputDelivery) {
	sessionID, text, origin := delivery.sessionID, delivery.text, delivery.origin
	m.daemon.life.Go("maybeGenerateSessionTitleFromPrompt", func() { m.daemon.maybeGenerateSessionTitleFromPrompt(sessionID, text, origin) })
}

// claimNextLocked hands a turn start the credit and title of words still waiting in the composer.
func (m *sessionInputModule) claimNextLocked(lane *sessionInputLane) bool {
	credit := lane.creditNextRun
	lane.creditNextRun = false
	if next := lane.titleNext; next != nil {
		lane.titleNext = nil
		m.titleLocked(*next)
	}
	return credit
}

func (lane *sessionInputLane) forfeitNextLocked() {
	lane.creditNextRun = false
	lane.titleNext = nil
}

func (m *sessionInputModule) recordOwedLocked(lane *sessionInputLane, sessionID string) {
	m.daemon.recordPlacedInputOwed(sessionID, lane.occupied && reportsTurnStarts(string(m.daemon.sessionAgent(sessionID))))
}

func (m *sessionInputModule) releaseComposerLocked(lane *sessionInputLane, sessionID string) {
	if !lane.occupied {
		return
	}
	lane.occupied = false
	m.recordOwedLocked(lane, sessionID)
}

func deliveryAllowedForPhase(placement sessionInputPlacement, state protocol.SessionState) error {
	if state == protocol.SessionStatePendingApproval {
		return errSessionInputBlockedByApproval
	}
	if placement == sessionInputWhenPromptReady && state != protocol.SessionStateIdle && state != protocol.SessionStateWaitingInput {
		return fmt.Errorf("session input requires a prompt-ready session, got %s", state)
	}
	return nil
}

func sessionInputPhaseAllows(placement sessionInputPlacement, state protocol.SessionState) bool {
	return deliveryAllowedForPhase(placement, state) == nil
}

func (m *sessionInputModule) observePromptSubmitted(sessionID string) (run sessionInputRunRef, credited, typed bool) {
	lane := m.lane(sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	typed = lane.userSubmit
	lane.userSubmit = false
	m.daemon.forgetUserInput(sessionID)
	m.releaseComposerLocked(lane, sessionID)
	current := m.ensureRunLocked(lane, sessionID)
	if m.claimNextLocked(lane) || typed {
		current.credited = true
	}
	return current.ref, current.credited, typed
}

func (m *sessionInputModule) ensureRunLocked(lane *sessionInputLane, sessionID string) *sessionInputRunState {
	if lane.run != nil {
		return lane.run
	}
	lane.epoch++
	lane.run = &sessionInputRunState{ref: sessionInputRunRef{sessionID: sessionID, epoch: lane.epoch}, credited: m.claimNextLocked(lane)}
	return lane.run
}

func (m *sessionInputModule) observePhase(sessionID string, phase protocol.SessionState) {
	lane := m.lane(sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	previous := lane.phase
	lane.phase = phase
	if lane.heldEnter && previous == protocol.SessionStatePendingApproval && phase != previous {
		m.holdEnterLocked(lane, sessionID, 0)
	}
	if phase == protocol.SessionStateWorking {
		if previous == protocol.SessionStatePendingApproval {
			lane.userSubmit = false
			m.daemon.forgetUserInput(sessionID)
		} else if lane.run == nil {
			m.releaseComposerLocked(lane, sessionID)
		}
		m.ensureRunLocked(lane, sessionID)
		return
	}
	if previous == protocol.SessionStateWorking && lane.userSubmit {
		lane.userSubmit = false
		m.daemon.forgetUserInput(sessionID)
	}
	lane.run = nil
}

func (m *sessionInputModule) currentUserRun(sessionID string) (sessionInputRunRef, bool) {
	lane := m.lane(sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if lane.run == nil || !lane.run.credited {
		return sessionInputRunRef{}, false
	}
	return lane.run.ref, true
}

// attn's own words earned credit at custody; this hook never matches prompt text.
func (d *Daemon) observePromptSubmitted(sessionID string, at time.Time) bool {
	run, credited, typed := d.sessionInputs().observePromptSubmitted(sessionID)
	d.markModelRequestStarted(sessionID, at)
	if credited {
		d.armAutoSettleForUserInput(run)
	}
	return typed
}

func (d *Daemon) markModelRequestStarted(sessionID string, at time.Time) bool {
	if d.store == nil || !d.store.MarkModelRequestStarted(sessionID, at) {
		return false
	}
	d.publishFact(FactSessionModelRequestStarted, sessionID, nil)
	return true
}

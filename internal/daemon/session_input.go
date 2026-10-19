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
	sessionID         protocol.SessionID
	text              string
	origin            sessionInputOrigin
	placement         sessionInputPlacement
	allowUserComposer bool
	bypassInitialGate bool
	resend            func()
}

func maintenanceSessionInput(domain string, key string, sessionID protocol.SessionID, text string, placement sessionInputPlacement) sessionInputDelivery {
	return sessionInputDelivery{
		id:        inputAttemptID(domain, key),
		sessionID: protocol.TrimID(sessionID),
		text:      text,
		origin:    maintenanceInput(domain),
		placement: placement,
	}
}

func userConversationSessionInput(key string, sessionID protocol.SessionID, text string, placement sessionInputPlacement) sessionInputDelivery {
	return sessionInputDelivery{
		id:        inputAttemptID("user-conversation", key),
		sessionID: protocol.TrimID(sessionID),
		text:      text,
		origin:    userConversationInput(),
		placement: placement,
	}
}

func annotationSessionInput(key string, sessionID protocol.SessionID, text string) sessionInputDelivery {
	delivery := userConversationSessionInput(key, sessionID, text, sessionInputAtTurnBoundary)
	delivery.allowUserComposer = true
	return delivery
}

type sessionInputRunRef struct {
	sessionID protocol.SessionID
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
	ref          sessionInputRunRef
	credited     bool
	notification bool
}

func (r *sessionInputRunState) startedByNotification() bool {
	return r.notification && !r.credited
}

type sessionInputLane struct {
	mu    *sync.Mutex
	entry *monitorEntry

	retries                  map[string]*sessionInputRetry
	run                      *sessionInputRunState
	placing                  bool
	creditNextRun            bool
	notifyNextRun            bool
	stoppedAfterNotification bool
	epoch                    uint64
	userSubmit               bool
	phase                    protocol.SessionState
	stopped                  bool
	running                  sync.WaitGroup
}

type sessionInputModule struct{ daemon *Daemon }

var (
	errSessionInputBlockedByApproval = errors.New("session input blocked by pending approval")
	errSessionInputComposerDirty     = errors.New("session input blocked by the user's composer")
	errSessionInputPlacingAnother    = errors.New("another session input is being placed")
	errSessionInputLaneClosed        = errors.New("the session's input lane is closed")
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

var sessionInputPlacingRetry = 3 * time.Second

func sessionInputRetryDelay(err error) (time.Duration, bool) {
	var quiet *sessionInputQuietError
	if errors.As(err, &quiet) {
		return quiet.retryAfter, true
	}
	if errors.Is(err, errSessionInputPlacingAnother) {
		return sessionInputPlacingRetry, true
	}
	return 0, false
}

func sessionInputDeferredError(err error) bool {
	return errors.Is(err, errSessionInputBlockedByApproval) ||
		errors.Is(err, errSessionInputComposerDirty) ||
		errors.Is(err, errSessionInputInitialPrompt)
}

func (d *Daemon) sessionInputs() sessionInputModule { return sessionInputModule{daemon: d} }
func (m sessionInputModule) lane(sessionID protocol.SessionID) *sessionInputLane {
	if e := m.daemon.monitorEntry(sessionID); e != nil {
		e.mu.Lock()
		lane := e.input
		e.mu.Unlock()
		return lane
	}
	return &sessionInputLane{mu: new(sync.Mutex), stopped: true}
}

func (m sessionInputModule) fenceSession(sessionID protocol.SessionID) {
	lane := m.lane(sessionID)
	lane.mu.Lock()
	lane.stopRetriesLocked()
	lane.mu.Unlock()
	lane.running.Wait()
}

func (m sessionInputModule) resetSession(sessionID protocol.SessionID) {
	e := m.daemon.monitorEntry(sessionID)
	if e == nil {
		return
	}
	e.mu.Lock()
	lane := e.input
	lane.stopRetriesLocked()
	e.mu.Unlock()
	lane.running.Wait()
	e.mu.Lock()
	e.input = &sessionInputLane{mu: &e.mu, entry: e, stopped: m.daemon.monitors().closed.Load()}
	e.mu.Unlock()
}

func (m sessionInputModule) armRetryLocked(lane *sessionInputLane, delivery sessionInputDelivery, err error) {
	after, retryable := sessionInputRetryDelay(err)
	if !retryable || delivery.resend == nil {
		return
	}
	m.scheduleLocked(lane, delivery.sessionID, delivery.id.String(), after, delivery.resend)
}

func (m sessionInputModule) scheduleLocked(lane *sessionInputLane, sessionID protocol.SessionID, key string, after time.Duration, resend func()) {
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

func (m sessionInputModule) fireRetry(sessionID protocol.SessionID, key string, self *sessionInputRetry) {
	lane := m.lane(sessionID)

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

func (m sessionInputModule) stopRetries() {
	r := m.daemon.monitors()
	r.closed.Store(true)
	entries := r.each()
	lanes := make([]*sessionInputLane, 0, len(entries))
	for _, e := range entries {
		e.mu.Lock()
		lanes = append(lanes, e.input)
		e.mu.Unlock()
	}

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

func (m sessionInputModule) try(ctx context.Context, delivery sessionInputDelivery) sessionInputAttempt {
	if delivery.id.String() == "" || protocol.TrimID(delivery.sessionID) == "" || strings.TrimSpace(delivery.text) == "" {
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

func (m sessionInputModule) place(ctx context.Context, delivery sessionInputDelivery) (sessionInputAttempt, sessionInputRunRef) {
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

	state := m.daemon.store.Get(delivery.sessionID)
	if state == nil {
		return deferred(fmt.Errorf("session %s is gone", delivery.sessionID))
	}
	if !delivery.bypassInitialGate && lane.entry.evidence.InitialPromptOwed {
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

func (m sessionInputModule) placeOverLinkLocked(ctx context.Context, lane *sessionInputLane, delivery sessionInputDelivery, state *protocol.Session) (sessionInputAttempt, bool) {
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

// tookLocked credits the user's words to the run going, or to the next turn start when none runs.
func (m sessionInputModule) tookLocked(lane *sessionInputLane, delivery sessionInputDelivery) sessionInputRunRef {
	if delivery.origin.kind == sessionInputOriginMaintenance && lane.run == nil {
		lane.notifyNextRun = true
	}
	if delivery.origin.kind != sessionInputOriginUserConversation {
		return sessionInputRunRef{}
	}
	m.titleLocked(delivery)
	if lane.run == nil {
		lane.creditNextRun = true
		return sessionInputRunRef{}
	}
	lane.run.credited = true
	return lane.run.ref
}

func (m sessionInputModule) titleLocked(delivery sessionInputDelivery) {
	sessionID, text, origin := delivery.sessionID, delivery.text, delivery.origin
	m.daemon.life.Go("maybeGenerateSessionTitleFromPrompt", func() { m.daemon.maybeGenerateSessionTitleFromPrompt(sessionID, text, origin) })
}

func (lane *sessionInputLane) claimNextLocked() bool {
	credit := lane.creditNextRun
	lane.creditNextRun = false
	return credit
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

func (m sessionInputModule) observePromptSubmitted(sessionID protocol.SessionID) (run sessionInputRunRef, credited, typed bool) {
	lane := m.lane(sessionID)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	typed = lane.userSubmit
	lane.userSubmit = false
	current := lane.ensureRunLocked(sessionID)
	if lane.claimNextLocked() || typed {
		current.credited = true
	}
	return current.ref, current.credited, typed
}

func (lane *sessionInputLane) ensureRunLocked(sessionID protocol.SessionID) *sessionInputRunState {
	if lane.run != nil {
		return lane.run
	}
	lane.epoch++
	lane.run = &sessionInputRunState{ref: sessionInputRunRef{sessionID: sessionID, epoch: lane.epoch}, credited: lane.claimNextLocked(), notification: lane.notifyNextRun}
	lane.notifyNextRun = false
	lane.stoppedAfterNotification = false
	return lane.run
}

func (lane *sessionInputLane) observePhaseLocked(sessionID protocol.SessionID, phase protocol.SessionState) {
	previous := lane.phase
	lane.phase = phase
	if phase == protocol.SessionStateWorking {
		if previous == protocol.SessionStatePendingApproval {
			lane.userSubmit = false
		}
		lane.ensureRunLocked(sessionID)
		return
	}
	if previous == protocol.SessionStateWorking {
		lane.userSubmit = false
	}
	if lane.run != nil {
		lane.stoppedAfterNotification = lane.run.startedByNotification()
	}
	lane.run = nil
}

func (lane *sessionInputLane) inNotificationRunLocked() bool {
	return lane.run != nil && lane.run.startedByNotification()
}

func (lane *sessionInputLane) stopFollowsNotificationLocked() bool {
	if lane.run != nil {
		return lane.run.startedByNotification()
	}
	return lane.stoppedAfterNotification
}

func (lane *sessionInputLane) currentUserRunLocked() (sessionInputRunRef, bool) {
	if lane.run == nil || !lane.run.credited {
		return sessionInputRunRef{}, false
	}
	return lane.run.ref, true
}

// attn's own words earned credit at custody; this hook never matches prompt text.
func (d *Daemon) observePromptSubmitted(sessionID protocol.SessionID, at time.Time) bool {
	run, credited, typed := d.sessionInputs().observePromptSubmitted(sessionID)
	d.markModelRequestStarted(sessionID, at)
	if credited {
		d.armAutoSettleForUserInput(run)
	}
	return typed
}

func (d *Daemon) markModelRequestStarted(sessionID protocol.SessionID, at time.Time) bool {
	if d.store == nil {
		return false
	}
	if entry := d.monitorEntry(sessionID); entry != nil {
		entry.mu.Lock()
		if at.IsZero() || !at.After(entry.lastModelRequestAt) {
			entry.mu.Unlock()
			return false
		}
		entry.lastModelRequestAt = at
		view := entry.viewLocked()
		entry.mu.Unlock()
		return entry.sink.Applied(view)
	} else if !d.store.MarkModelRequestStarted(sessionID, at) {
		return false
	}
	d.publishFact(FactSessionModelRequestStarted, string(sessionID), nil)
	return true
}

package sessionmonitor

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

type Stage uint8

const (
	Deferred Stage = iota
	Placed
	Failed
)

type Placement uint8

const (
	WhenPromptReady Placement = iota
	AtTurnBoundary
)

type originKind uint8

const (
	originUnknown originKind = iota
	originUserConversation
	originMaintenance
)

type inputOrigin struct {
	kind   originKind
	source string
}

func (o inputOrigin) voice() harness.Voice {
	if o.kind == originMaintenance {
		return harness.VoiceAttn
	}
	return harness.VoiceUser
}

type AttemptID struct {
	domain string
	key    string
}

func NewAttemptID(domain, key string) AttemptID {
	return AttemptID{domain: strings.TrimSpace(domain), key: strings.TrimSpace(key)}
}

func (id AttemptID) String() string {
	if id.domain == "" || id.key == "" {
		return ""
	}
	return id.domain + "/" + id.key
}

type Delivery struct {
	ID                AttemptID
	Text              string
	origin            inputOrigin
	Placement         Placement
	AllowUserComposer bool
	BypassInitialGate bool
	Resend            func()
}

func MaintenanceInput(domain string, key string, text string, placement Placement) Delivery {
	return Delivery{
		ID:        NewAttemptID(domain, key),
		Text:      text,
		origin:    inputOrigin{kind: originMaintenance, source: strings.TrimSpace(domain)},
		Placement: placement,
	}
}

func UserConversationInput(key string, text string, placement Placement) Delivery {
	return Delivery{
		ID:        NewAttemptID("user-conversation", key),
		Text:      text,
		origin:    inputOrigin{kind: originUserConversation},
		Placement: placement,
	}
}

func AnnotationInput(key string, text string) Delivery {
	delivery := UserConversationInput(key, text, AtTurnBoundary)
	delivery.AllowUserComposer = true
	return delivery
}

type runRef struct {
	epoch uint64
}

func (r runRef) valid() bool { return r.epoch > 0 }

// Custody is a delivery's only result: never confirm afterwards that the harness used it.
type Attempt struct {
	Stage Stage
	At    time.Time
	Err   error
}

type runState struct {
	ref          runRef
	credited     bool
	notification bool
}

func (r *runState) startedByNotification() bool {
	return r.notification && !r.credited
}

type lane struct {
	mu    *sync.Mutex
	entry *Entry

	retries                  map[string]*retry
	run                      *runState
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

var (
	ErrBlockedByApproval = errors.New("session input blocked by pending approval")
	ErrComposerDirty     = errors.New("session input blocked by the user's composer")
	errPlacingAnother    = errors.New("another session input is being placed")
	ErrLaneClosed        = errors.New("the session's input lane is closed")
	errInitialPrompt     = errors.New("session input blocked while the initial prompt is pending")
)

var QuietWindow = 30 * time.Second

type quietError struct{ retryAfter time.Duration }

func (e *quietError) Error() string {
	return fmt.Sprintf("%v (retry in %s)", ErrComposerDirty, e.retryAfter)
}

func (e *quietError) Unwrap() error { return ErrComposerDirty }

type retry struct {
	timer  *time.Timer
	resend func()
}

var PlacingRetry = 3 * time.Second

func RetryDelay(err error) (time.Duration, bool) {
	var quiet *quietError
	if errors.As(err, &quiet) {
		return quiet.retryAfter, true
	}
	if errors.Is(err, errPlacingAnother) {
		return PlacingRetry, true
	}
	return 0, false
}

func DeferredError(err error) bool {
	return errors.Is(err, ErrBlockedByApproval) ||
		errors.Is(err, ErrComposerDirty) ||
		errors.Is(err, errInitialPrompt)
}

func (e *Entry) FenceInput() {
	if e == nil {
		return
	}
	e.mu.Lock()
	lane := e.input
	lane.stopRetriesLocked()
	e.mu.Unlock()
	lane.running.Wait()
}

func (e *Entry) ResetInput() {
	if e == nil {
		return
	}
	e.FenceInput()
	e.mu.Lock()
	e.input = &lane{mu: &e.mu, entry: e, stopped: e.registry.closed.Load()}
	e.mu.Unlock()
}

func (l *lane) armRetryLocked(delivery Delivery, err error) {
	after, retryable := RetryDelay(err)
	if !retryable || delivery.Resend == nil {
		return
	}
	l.scheduleLocked(delivery.ID.String(), after, delivery.Resend)
}

func (l *lane) scheduleLocked(key string, after time.Duration, resend func()) {
	host := l.entry.registry.host
	if l.stopped || host.Ended() {
		return
	}
	if l.retries == nil {
		l.retries = make(map[string]*retry)
	}
	if existing := l.retries[key]; existing != nil {
		existing.timer.Stop()
	}
	entry := &retry{resend: resend}
	entry.timer = host.AfterFunc("sessionInputRetry", after, func() { l.entry.fireRetry(key, entry) })
	l.retries[key] = entry
}

func (e *Entry) fireRetry(key string, self *retry) {
	e.mu.Lock()
	l := e.input
	owner := !l.stopped && l.retries[key] == self
	if owner {
		delete(l.retries, key)
		l.running.Add(1)
	}
	e.mu.Unlock()
	if !owner {
		return
	}
	defer l.running.Done()
	self.resend()
}

func (l *lane) stopRetriesLocked() {
	l.stopped = true
	for key, entry := range l.retries {
		entry.timer.Stop()
		delete(l.retries, key)
	}
}

func (e *Entry) Place(ctx context.Context, delivery Delivery) Attempt {
	if e == nil {
		return Attempt{Stage: Deferred, Err: ErrLaneClosed}
	}
	if delivery.ID.String() == "" || strings.TrimSpace(delivery.Text) == "" {
		return Attempt{Stage: Failed, Err: errors.New("session input needs an attempt id and text")}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(e.registry.host.Context(), cancel)()
	attempt, credited := e.place(ctx, delivery)
	if credited.valid() {
		e.armAutoSettleForUserInput(credited)
	}
	return attempt
}

func (e *Entry) place(ctx context.Context, delivery Delivery) (Attempt, runRef) {
	e.mu.Lock()
	defer e.mu.Unlock()
	l := e.input
	deferred := func(err error) (Attempt, runRef) {
		return Attempt{Stage: Deferred, Err: err}, runRef{}
	}
	if l.stopped {
		return deferred(ErrLaneClosed)
	}
	if l.placing {
		l.armRetryLocked(delivery, errPlacingAnother)
		return deferred(errPlacingAnother)
	}
	if !delivery.BypassInitialGate && e.evidence.InitialPromptOwed {
		return deferred(errInitialPrompt)
	}
	if err := allowedForPhase(delivery.Placement, e.state.State); err != nil {
		return deferred(err)
	}

	var attempt Attempt
	handled := false
	if e.registry.host.Link(e, delivery.origin.voice()) != nil {
		attempt, handled = l.placeOverLinkLocked(ctx, delivery)
	}
	if !handled {
		attempt = l.placePTYLocked(ctx, delivery)
	}
	if attempt.Stage != Placed {
		return attempt, runRef{}
	}
	return attempt, l.tookLocked(delivery)
}

func (l *lane) placeOverLinkLocked(ctx context.Context, delivery Delivery) (Attempt, bool) {
	voice := delivery.origin.voice()
	l.placing = true
	l.mu.Unlock()
	// Resolve again after unlocking: a link gone meanwhile hands the input to the PTY.
	link := l.entry.registry.host.Link(l.entry, voice)
	var custody harness.Custody
	if link != nil {
		custody = link.Deliver(ctx, harness.Input{ID: delivery.ID.String(), Text: delivery.Text, Voice: voice})
	}
	l.mu.Lock()
	l.placing = false
	if link == nil {
		return Attempt{}, false
	}
	if !custody.Taken {
		return Attempt{Stage: Deferred, Err: errors.New(custody.Reason)}, true
	}
	return Attempt{Stage: Placed, At: custody.At}, true
}

// tookLocked credits the user's words to the run going, or to the next turn start when none runs.
func (l *lane) tookLocked(delivery Delivery) runRef {
	if delivery.origin.kind == originMaintenance && l.run == nil {
		l.notifyNextRun = true
	}
	if delivery.origin.kind != originUserConversation {
		return runRef{}
	}
	l.titleLocked(delivery)
	if l.run == nil {
		l.creditNextRun = true
		return runRef{}
	}
	l.run.credited = true
	return l.run.ref
}

func (l *lane) titleLocked(delivery Delivery) {
	e, text, user := l.entry, delivery.Text, delivery.origin.kind == originUserConversation
	e.registry.host.Go("maybeGenerateSessionTitleFromPrompt", func() { e.TitleFromPrompt(text, user) })
}

func (l *lane) claimNextLocked() bool {
	credit := l.creditNextRun
	l.creditNextRun = false
	return credit
}

func allowedForPhase(placement Placement, state protocol.SessionState) error {
	if state == protocol.SessionStatePendingApproval {
		return ErrBlockedByApproval
	}
	if placement == WhenPromptReady && state != protocol.SessionStateIdle && state != protocol.SessionStateWaitingInput {
		return fmt.Errorf("session input requires a prompt-ready session, got %s", state)
	}
	return nil
}

func PhaseAllows(placement Placement, state protocol.SessionState) bool {
	return allowedForPhase(placement, state) == nil
}

// attn's own words earned credit at custody; this hook never matches prompt text.
func (e *Entry) ObservePromptSubmitted(at time.Time) (typed bool) {
	if e == nil {
		return false
	}
	e.mu.Lock()
	l := e.input
	typed = l.userSubmit
	l.userSubmit = false
	current := l.ensureRunLocked()
	if l.claimNextLocked() || typed {
		current.credited = true
	}
	run, credited := current.ref, current.credited
	e.mu.Unlock()
	e.MarkModelRequestStarted(at)
	if credited {
		e.armAutoSettleForUserInput(run)
	}
	return typed
}

func (l *lane) ensureRunLocked() *runState {
	if l.run != nil {
		return l.run
	}
	l.epoch++
	l.run = &runState{ref: runRef{epoch: l.epoch}, credited: l.claimNextLocked(), notification: l.notifyNextRun}
	l.notifyNextRun = false
	l.stoppedAfterNotification = false
	return l.run
}

func (l *lane) observePhaseLocked(phase protocol.SessionState) {
	previous := l.phase
	l.phase = phase
	if phase == protocol.SessionStateWorking {
		if previous == protocol.SessionStatePendingApproval {
			l.userSubmit = false
		}
		l.ensureRunLocked()
		return
	}
	if previous == protocol.SessionStateWorking {
		l.userSubmit = false
	}
	if l.run != nil {
		l.stoppedAfterNotification = l.run.startedByNotification()
	}
	l.run = nil
}

func (l *lane) inNotificationRunLocked() bool {
	return l.run != nil && l.run.startedByNotification()
}

func (l *lane) stopFollowsNotificationLocked() bool {
	if l.run != nil {
		return l.run.startedByNotification()
	}
	return l.stoppedAfterNotification
}

func (e *Entry) LatestRun() uint64 {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.input.epoch
}

func (l *lane) currentUserRunLocked() (runRef, bool) {
	if l.run == nil || !l.run.credited {
		return runRef{}, false
	}
	return l.run.ref, true
}

func (e *Entry) MarkModelRequestStarted(at time.Time) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	if at.IsZero() || !at.After(e.lastModelRequestAt) {
		e.mu.Unlock()
		return false
	}
	e.lastModelRequestAt = at
	view := e.viewLocked()
	e.mu.Unlock()
	return e.publish(view)
}

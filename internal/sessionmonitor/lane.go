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
	"github.com/victorarias/attn/internal/sessionstate"
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
	WhenWorkingOrPromptReady
	AtTurnBoundary
)

type originKind uint8

const (
	originUnknown originKind = iota
	originUserConversation
	originMaintenance
)

type Maintenance string

const (
	InboxRing       Maintenance = "inbox-ring"
	CrewHeartbeat   Maintenance = "crew-heartbeat"
	CompactingClose Maintenance = "compacting-close"
)

type inputOrigin struct {
	kind   originKind
	source Maintenance
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
	ID                   AttemptID
	Text                 string
	origin               inputOrigin
	Placement            Placement
	AllowUserComposer    bool
	BypassInitialGate    bool
	RequireEmptyComposer bool
	Compact              bool
	Ring                 *protocol.InboxRingRecord
}

func MaintenanceInput(domain Maintenance, key string, text string, placement Placement) Delivery {
	return Delivery{
		ID:        NewAttemptID(string(domain), key),
		Text:      text,
		origin:    inputOrigin{kind: originMaintenance, source: domain},
		Placement: placement,
	}
}

func CompactInput(key string) Delivery {
	delivery := MaintenanceInput(CompactingClose, key, "", WhenPromptReady)
	delivery.Compact = true
	return delivery
}

func UserConversationInput(key string, text string, placement Placement) Delivery {
	return Delivery{
		ID:        NewAttemptID("user-conversation", key),
		Text:      text,
		origin:    inputOrigin{kind: originUserConversation},
		Placement: placement,
	}
}

func ComposedInput(key string, text string) Delivery {
	delivery := UserConversationInput(key, text, AtTurnBoundary)
	delivery.AllowUserComposer = true
	return delivery
}

func ReturnedUserInput(key string, text string) Delivery {
	delivery := UserConversationInput(key, text, WhenPromptReady)
	delivery.ID = NewAttemptID("claude-return", key)
	delivery.AllowUserComposer = true
	delivery.RequireEmptyComposer = true
	return delivery
}

type runRef struct {
	epoch uint64
}

func (r runRef) valid() bool { return r.epoch > 0 }

// Custody is a delivery's only result: never confirm afterwards that the harness used it.
type Attempt struct {
	Stage   Stage
	At      time.Time
	Err     error
	Held    bool
	steered bool
}

type runState struct {
	ref          runRef
	credited     bool
	notification Maintenance
	launch       bool
	prompted     bool
}

func (r *runState) startedByNotification() bool {
	return r.notification != "" && !r.credited
}

type lane struct {
	mu      *sync.Mutex
	monitor *Monitor

	run                      *runState
	questionInTurn           bool
	placing                  bool
	placementEnded           *sync.Cond
	placed                   map[string]Attempt
	creditNextRun            bool
	notifyNextRun            Maintenance
	launchNextRun            bool
	stoppedAfterNotification bool
	epoch                    uint64
	userSubmit               bool
	userDraft                bool
	phase                    protocol.SessionState
	stopped                  bool
	closing                  bool
}

var (
	ErrBlockedByApproval = errors.New("session input blocked by pending approval")
	ErrComposerDirty     = errors.New("session input blocked by the user's composer")
	ErrLaneClosed        = errors.New("the session's input lane is closed")
	ErrSessionStopped    = errors.New("the user stopped the session; attn's input waits for the user's next prompt")
	ErrSessionClosing    = errors.New("the session is compacting before it closes; attn's input waits until it has closed")
	ErrInputCancelsClose = errors.New("not sent: the session was compacting before it closed; the close is cancelled, so send it again once it is idle")
	errInitialPrompt     = errors.New("session input blocked while the initial prompt is pending")
)

var QuietWindow = 30 * time.Second

type quietError struct{ retryAfter time.Duration }

func (e *quietError) Error() string {
	return fmt.Sprintf("%v (retry in %s)", ErrComposerDirty, e.retryAfter)
}

func (e *quietError) Unwrap() error { return ErrComposerDirty }

const MaxPlacedDeliveries = 65_536

func RetryDelay(err error) (time.Duration, bool) {
	var quiet *quietError
	if errors.As(err, &quiet) {
		return quiet.retryAfter, true
	}
	return 0, false
}

func DeferredError(err error) bool {
	return errors.Is(err, ErrBlockedByApproval) ||
		errors.Is(err, ErrComposerDirty) ||
		errors.Is(err, errInitialPrompt) ||
		errors.Is(err, ErrSessionStopped) ||
		errors.Is(err, ErrSessionClosing)
}

func (m *Monitor) FenceInput() {
	if m == nil {
		return
	}
	m.mu.Lock()
	lane := m.input
	lane.stopLocked()
	m.mu.Unlock()
}

func (m *Monitor) ResetInput() {
	if m == nil {
		return
	}
	m.FenceInput()
	m.mu.Lock()
	m.input = m.freshLaneLocked()
	m.mu.Unlock()
}

func (m *Monitor) freshLaneLocked() *lane {
	l := &lane{mu: &m.mu, monitor: m, placementEnded: sync.NewCond(&m.mu), stopped: m.registry.closed.Load()}
	l.observePhaseLocked(m.state.State)
	return l
}

func (l *lane) stopLocked() { l.stopped = true }

func (m *Monitor) Place(ctx context.Context, delivery Delivery) Attempt {
	if m == nil {
		return Attempt{Stage: Deferred, Err: ErrLaneClosed}
	}
	if delivery.ID.String() == "" || (strings.TrimSpace(delivery.Text) == "") != delivery.Compact {
		return Attempt{Stage: Failed, Err: errors.New("session input needs an attempt id, and text unless it is a compact")}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(m.registry.host.Context(), cancel)()
	attempt, credited := m.place(ctx, delivery)
	if credited.valid() {
		m.armAutoSettleForUserInput(credited)
	}
	if attempt.Stage == Placed && delivery.origin.kind == originMaintenance {
		m.settleForRunningNotification()
	}
	return attempt
}

func (m *Monitor) settleForRunningNotification() {
	m.mu.Lock()
	if m.state.State != protocol.SessionStateWorking {
		m.mu.Unlock()
		return
	}
	m.syncAutoSettleLocked()
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

func (m *Monitor) place(ctx context.Context, delivery Delivery) (Attempt, runRef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.input.placing {
		m.input.placementEnded.Wait()
	}
	l := m.input
	if placed, ok := l.placed[delivery.ID.String()]; ok {
		return placed, runRef{}
	}
	deferred := func(err error) (Attempt, runRef) {
		return Attempt{Stage: Deferred, Err: err}, runRef{}
	}
	if l.stopped {
		return deferred(ErrLaneClosed)
	}
	if l.closing && delivery.origin.kind == originUserConversation {
		m.userInputs++
		return Attempt{Stage: Failed, Err: ErrInputCancelsClose}, runRef{}
	}
	if l.closing {
		return deferred(ErrSessionClosing)
	}
	if !delivery.BypassInitialGate && m.evidence.InitialPromptOwed {
		return deferred(errInitialPrompt)
	}
	phaseErr := allowedForPhase(delivery.Placement, m.state.State, m.reason)
	duringQuestion := errors.Is(phaseErr, ErrBlockedByApproval) && l.questionInTurn && allowedForPhase(delivery.Placement, protocol.SessionStateWorking, "") == nil
	if phaseErr != nil && !duringQuestion {
		return deferred(phaseErr)
	}
	if len(l.placed) >= MaxPlacedDeliveries {
		return Attempt{Stage: Failed, Err: fmt.Errorf("session %s: max_placed_deliveries=%d, asked for %d; reload the agent to reset its input lane", m.session, MaxPlacedDeliveries, len(l.placed)+1)}, runRef{}
	}

	steering := l.run != nil || l.questionInTurn
	epochAtPlacing := l.epoch
	var attempt Attempt
	handled := false
	attempt, handled = l.placeOverLinkLocked(ctx, delivery, duringQuestion)
	if !handled && delivery.Compact {
		return Attempt{Stage: Failed, Err: errors.New("the session has no link that compacts its conversation")}, runRef{}
	}
	if !handled {
		if duringQuestion {
			return deferred(ErrBlockedByApproval)
		}
		attempt = l.placePTYLocked(ctx, delivery)
		attempt.steered = l.run != nil
	}
	if attempt.Stage != Placed {
		return attempt, runRef{}
	}
	if l.placed == nil {
		l.placed = make(map[string]Attempt)
	}
	l.placed[delivery.ID.String()] = attempt
	if delivery.Compact {
		l.closing = true
	}
	if delivery.origin.kind == originUserConversation {
		m.userInputs++
	}
	if steering {
		m.runSteeredLocked()
	}
	credited := l.tookLocked(delivery)
	if !steering && !attempt.steered {
		if delivery.origin.kind == originMaintenance {
			l.notificationTookLocked(delivery.origin.source, epochAtPlacing)
		}
		if l.epoch != epochAtPlacing {
			m.runStartedDuringPlacementLocked()
		}
	}
	return attempt, credited
}

func (l *lane) notificationTookLocked(source Maintenance, epochAtPlacing uint64) {
	switch {
	case l.epoch == epochAtPlacing:
		l.notifyNextRun = source
	case l.run != nil:
		l.run.notification = source
	}
}

func (l *lane) placeOverLinkLocked(ctx context.Context, delivery Delivery, duringQuestion bool) (Attempt, bool) {
	voice := delivery.origin.voice()
	l.placing = true
	l.mu.Unlock()
	// Resolve again after unlocking: a link gone meanwhile hands the input to the PTY.
	link := l.monitor.registry.host.Link(l.monitor, voice)
	if _, safe := link.(harness.QuestionSafeLink); duringQuestion && !safe {
		link = nil
	}
	in := harness.Input{ID: delivery.ID.String(), Text: delivery.Text, Voice: voice, Ring: delivery.Ring}
	var custody harness.Custody
	if compactor, ok := link.(harness.Compactor); delivery.Compact && ok {
		custody = compactor.Compact(ctx, in)
	} else if delivery.Compact {
		link = nil
	} else if link != nil {
		custody = link.Deliver(ctx, in)
	}
	l.mu.Lock()
	l.placing = false
	l.placementEnded.Broadcast()
	if link == nil {
		return Attempt{}, false
	}
	if custody.Stopped {
		if voice == harness.VoiceUser {
			l.userDraft = !custody.ComposerEmpty
			return Attempt{}, false
		}
		return Attempt{Stage: Deferred, Err: ErrSessionStopped}, true
	}
	if !custody.Taken {
		return Attempt{Stage: Deferred, Err: errors.New(custody.Reason)}, true
	}
	return Attempt{Stage: Placed, At: custody.At, steered: custody.Steered}, true
}

// tookLocked credits the user's words to the run going, or to the next turn start when none runs.
func (l *lane) tookLocked(delivery Delivery) runRef {
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
	m, text, user := l.monitor, delivery.Text, delivery.origin.kind == originUserConversation
	m.registry.host.Go("maybeGenerateSessionTitleFromPrompt", func() { m.TitleFromPrompt(text, user) })
}

func (l *lane) claimNextLocked() bool {
	credit := l.creditNextRun
	l.creditNextRun = false
	return credit
}

func PromptReady(state protocol.SessionState, reason string) bool {
	switch state {
	case protocol.SessionStateIdle, protocol.SessionStateWaitingInput, protocol.SessionStateUnknown:
		return true
	case protocol.SessionStateWorking:
		return reason == string(sessionstate.ReasonBackgroundParked)
	}
	return false
}

func allowedForPhase(placement Placement, state protocol.SessionState, reason string) error {
	if state == protocol.SessionStatePendingApproval {
		return ErrBlockedByApproval
	}
	promptReady := PromptReady(state, reason)
	if placement == WhenPromptReady && !promptReady {
		return fmt.Errorf("session input requires a prompt-ready session, got %s", state)
	}
	if placement == WhenWorkingOrPromptReady && !promptReady && state != protocol.SessionStateWorking {
		return fmt.Errorf("session input requires a working or prompt-ready session, got %s", state)
	}
	return nil
}

func PhaseAllows(placement Placement, state protocol.SessionState) bool {
	return allowedForPhase(placement, state, "") == nil
}

// attn's own words earned credit at custody; this hook never matches prompt text.
func (m *Monitor) ObservePromptSubmitted(at time.Time) (typed bool) {
	if m == nil {
		return false
	}
	m.mu.Lock()
	l := m.input
	typed = l.userSubmit
	l.userSubmit = false
	if typed {
		l.userDraft = false
	}
	current := l.ensureRunLocked()
	if l.claimNextLocked() || typed {
		current.credited = true
	}
	current.prompted = true
	m.runPromptedLocked()
	run, credited := current.ref, current.credited
	m.mu.Unlock()
	m.MarkModelRequestStarted(at)
	if credited {
		m.armAutoSettleForUserInput(run)
	}
	return typed
}

func (l *lane) ensureRunLocked() *runState {
	if l.run != nil {
		return l.run
	}
	l.epoch++
	l.run = &runState{ref: runRef{epoch: l.epoch}, credited: l.claimNextLocked(), notification: l.notifyNextRun, launch: l.launchNextRun}
	l.notifyNextRun, l.launchNextRun = "", false
	l.stoppedAfterNotification = false
	return l.run
}

func (l *lane) launchPromptTakenLocked() {
	if l.run != nil {
		l.run.launch = true
		return
	}
	l.launchNextRun = true
}

func (l *lane) observePhaseLocked(phase protocol.SessionState) {
	previous := l.phase
	l.phase = phase
	l.questionInTurn = phase == protocol.SessionStatePendingApproval && (previous == protocol.SessionStateWorking || l.questionInTurn)
	if phase == protocol.SessionStateWorking {
		if previous == protocol.SessionStatePendingApproval {
			l.userSubmit = false
		}
		l.ensureRunLocked()
		return
	}
	if previous == protocol.SessionStateWorking && !ReportsTurnStarts(string(l.monitor.agent)) {
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

func (m *Monitor) ReopenInput() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.input.closing = false
}

func (m *Monitor) UserInputs() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userInputs
}

func (m *Monitor) HasUserDraft() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.input.userDraft
}

func (m *Monitor) LatestRun() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.input.epoch
}

func (l *lane) currentUserRunLocked() (runRef, bool) {
	if l.run == nil || !l.run.credited {
		return runRef{}, false
	}
	return l.run.ref, true
}

func (m *Monitor) MarkModelRequestStarted(at time.Time) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if at.IsZero() || !at.After(m.lastModelRequestAt) {
		m.mu.Unlock()
		return false
	}
	m.lastModelRequestAt = at
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

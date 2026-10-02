package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

var inboxRingText = prompts.RenderText("session", "inbox-notification", nil)
var errInboxNoUnread = errors.New("agent inbox has no unread items to wake for")
var errInboxNoPromptReader = errors.New("agent inbox recipient is a shell pane")
var errInboxDoorbellOutstanding = errors.New("agent inbox ring already outstanding")
var errInboxDoorbellInFlight = errors.New("agent inbox ring already being placed")

func sessionReadsInboxDoorbells(session *protocol.Session) bool {
	return session != nil && !strings.EqualFold(strings.TrimSpace(string(session.Agent)), protocol.AgentShellValue)
}

type inboxDeliveryState struct {
	mu          sync.Mutex
	timer       *time.Timer
	stopped     bool
	wakeSession string
}

func (d *Daemon) inboxState(a inbox.Address) *inboxDeliveryState {
	d.inboxMu.Lock()
	defer d.inboxMu.Unlock()
	if d.inboxStates == nil {
		d.inboxStates = make(map[inbox.Address]*inboxDeliveryState)
	}
	state := d.inboxStates[a]
	if state == nil {
		state = &inboxDeliveryState{}
		d.inboxStates[a] = state
	}
	return state
}
func (d *Daemon) kickInbox(a inbox.Address) {
	d.life.Go("inbox", func() {
		select {
		case <-d.recoverySettledSignal():
		case <-d.life.Done():
			return
		}
		if _, err := d.deliverInbox(a); err != nil {
			d.logf("inbox: %s: %v", a, err)
		}
	})
}
func (d *Daemon) inboxHolder(a inbox.Address) *protocol.Session {
	id := a.SessionID()
	if member := a.MemberID(); member != "" {
		id, _ = d.crewSessionBoundTo(member)
	}
	if a == inbox.ToChief() {
		id = d.chiefOfStaffSessionID()
	}
	return d.store.Get(id)
}
func (d *Daemon) lockInboxState(a inbox.Address) *inboxDeliveryState {
	for {
		state := d.inboxState(a)
		state.mu.Lock()
		d.inboxMu.Lock()
		current := d.inboxStates[a] == state
		d.inboxMu.Unlock()
		if current {
			return state
		}
		state.mu.Unlock()
	}
}
func (d *Daemon) deliverInbox(a inbox.Address) (inbox.Receipt, error) {
	if d.isRecovering() {
		d.kickInbox(a)
		return inbox.Receipt{Detail: "queued until daemon recovery completes"}, nil
	}
	state := d.lockInboxState(a)
	defer state.mu.Unlock()
	return d.deliverInboxLocked(a, state)
}
func (d *Daemon) deliverInboxLocked(a inbox.Address, state *inboxDeliveryState) (inbox.Receipt, error) {
	receipt := inbox.Receipt{}
	defer func() {
		if state.timer != nil || state.wakeSession != "" {
			return
		}
		d.inboxMu.Lock()
		if d.inboxStates[a] == state {
			delete(d.inboxStates, a)
		}
		d.inboxMu.Unlock()
	}()
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	if state.stopped || d.life.Ended() {
		return receipt, nil
	}
	{
		d.lockGardenRoles()
		err := d.discardIneligibleGardenSeedBellsLocked(a)
		d.unlockGardenRoles()
		if err != nil {
			return receipt, err
		}
	}
	attempt, err := d.store.InboxAttempt(a)
	if err != nil {
		return receipt, err
	}
	if attempt.Unread == 0 {
		state.wakeSession = ""
		return receipt, nil
	}
	holder := d.inboxHolder(a)
	finishingWake := holder != nil && holder.ID == state.wakeSession
	if holder == nil || !finishingWake {
		state.wakeSession = ""
	}
	if !finishingWake {
		if attempt.Live == 0 {
			return receipt, nil
		}
		now := time.Now()
		due := inbox.Due(attempt.Last, now)
		if due.After(now) {
			d.armInboxLocked(a, state, due.Sub(now))
			receipt.Outstanding = holder != nil && a.MemberID() == ""
			receipt.Detail = agentMessageQueuedDetail(errInboxDoorbellOutstanding)
			if memberID := a.MemberID(); memberID != "" && holder == nil {
				if member, _, err := d.crewMember(memberID); err == nil {
					ledger := d.crewWakeLedger()
					ledger.Stamps = parseWakeStamps(member.AutonomousWakes)
					if _, refusal := ledger.Allows(memberID, now); refusal != nil {
						receipt.Detail = refusal.Error()
					}
				}
			}
			return receipt, nil
		}
		if holder == nil {
			if member := a.MemberID(); member != "" {
				d.crewWakeMu.Lock()
				if d.inboxHolder(a) != nil {
					d.crewWakeMu.Unlock()
					d.kickInbox(a)
					return receipt, nil
				}
				result, err := d.crewWakeDayWithChargeLocked(member, "", true, func() error {
					started, err := d.store.StampInboxAttempt(a, now)
					if err != nil {
						return err
					}
					if !started {
						return errInboxNoUnread
					}
					d.logInboxExhaustion(a)
					return nil
				})
				d.crewWakeMu.Unlock()
				if errors.Is(err, errInboxNoUnread) {
					return receipt, nil
				}
				if err != nil {
					state.wakeSession = ""
					d.logf("inbox: wake %s refused: %v", a, err)
					receipt.Detail = err.Error()
					d.armInboxLocked(a, state, inbox.AttemptDelay)
					return receipt, nil
				}
				if result.AlreadyAwake {
					state.wakeSession = ""
					d.kickInbox(a)
					return receipt, nil
				}
				state.wakeSession = result.SessionID
				receipt.Detail = fmt.Sprintf("woke %s in session %s; notification queued until it reaches a safe prompt", crew.DisplayName(member), shortSessionID(result.SessionID))
				return receipt, nil
			}
			if a == inbox.ToChief() {
				receipt.Detail = "no Chief yet; waits for the next Chief"
			} else {
				receipt.Detail = "queued (recipient is gone; waits for it to return)"
			}
			return receipt, nil
		}
	}
	if !sessionReadsInboxDoorbells(holder) {
		receipt.Detail = agentMessageQueuedDetail(errInboxNoPromptReader)
		return receipt, nil
	}
	key := uuid.NewString()
	id := inputAttemptID("inbox-ring", key)
	input := maintenanceSessionInput("inbox-ring", key, holder.ID, inboxRingText, sessionInputWhenPromptReady)
	input.bypassInitialGate = true
	placement := d.sessionInputs().try(context.Background(), input)
	if placement.err == nil && (placement.stage == sessionInputPlaced || placement.stage == sessionInputTaken) {
		d.sessionInputs().forget(holder.ID, id)
		now := time.Now()
		if err := d.store.RingInbox(a, finishingWake, now); err != nil {
			return receipt, err
		}
		state.wakeSession = ""
		if !finishingWake {
			d.logInboxExhaustion(a)
		}
		d.armInboxLocked(a, state, inbox.AttemptDelay)
		receipt.Rang = true
		receipt.Detail = "notified " + sessionDisplayName(holder)
		return receipt, nil
	}
	receipt.Detail = agentMessageQueuedDetail(placement.err)
	delay, retry := sessionInputRetryDelay(placement.err)
	if retry {
		d.armInboxLocked(a, state, delay)
	}
	return receipt, nil
}
func (d *Daemon) logInboxExhaustion(a inbox.Address) {
	attempt, err := d.store.InboxAttempt(a)
	if err == nil && attempt.Unread > 0 && attempt.Live == 0 {
		d.logf("inbox: stopped ringing %s after %d attempts (inbox.MaxAttempts=%d); %d unread wait for a read or a new item", a, inbox.MaxAttempts, inbox.MaxAttempts, attempt.Unread)
	}
}
func (d *Daemon) armInboxLocked(a inbox.Address, state *inboxDeliveryState, delay time.Duration) {
	if state.timer != nil {
		state.timer.Stop()
	}
	var timer *time.Timer
	timer = d.life.AfterFunc("inbox", delay, func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.timer != timer || state.stopped {
			return
		}
		state.timer = nil
		if _, err := d.deliverInboxLocked(a, state); err != nil {
			d.logf("inbox: %s: %v", a, err)
		}
	})
	state.timer = timer
}
func (d *Daemon) recoverInbox() {
	addresses, err := d.store.UnreadInboxAddresses()
	if err != nil {
		d.logf("inbox: recover: %v", err)
		return
	}
	for _, a := range addresses {
		d.kickInbox(a)
	}
}
func (d *Daemon) kickSessionInbox(sessionID, state string) {
	if sessionInputPhaseAllows(sessionInputWhenPromptReady, protocol.SessionState(state)) {
		d.kickInboxAfterCommit(d.inboxAddressesOf(sessionID)...)
	}
}
func (d *Daemon) subscribeInboxFacts() {

	d.inboxUnsubscribe = d.eventBus.Subscribe(bus.Filter{FactCrewBound, FactCrewReleased, FactSessionChiefRoleChanged, FactSessionRegistered, FactSessionUnregistered, FactSessionClosed, FactSessionPTYExited}, func(ev bus.Event) {
		switch ev.Name {
		case FactCrewBound, FactCrewReleased:
			d.kickInbox(inbox.ToMember(ev.Subject))
		case FactSessionChiefRoleChanged:
			d.kickInbox(inbox.ToChief())
		default:
			// Holder resolution runs outside publishMu, through lifetime work.
			d.life.Go("inbox-holder-change", func() { d.kickInboxAfterCommit(d.inboxAddressesOf(ev.Subject)...); d.kickInbox(inbox.ToChief()) })
		}
	})
}
func (d *Daemon) stopInbox() {
	d.inboxMu.Lock()
	states := make([]*inboxDeliveryState, 0, len(d.inboxStates))
	for _, state := range d.inboxStates {
		states = append(states, state)
	}
	d.inboxMu.Unlock()
	for _, state := range states {
		state.mu.Lock()
		state.stopped = true
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
		state.mu.Unlock()
	}
}

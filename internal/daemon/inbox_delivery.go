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
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

var inboxRingText = prompts.RenderText("session", "inbox-notification", nil)
var errInboxNoUnread = errors.New("agent inbox has no unread items to wake for")
var errInboxNoPromptReader = errors.New("agent inbox recipient is a shell pane")
var errInboxDoorbellOutstanding = errors.New("agent inbox ring already outstanding")
var errInboxDoorbellInFlight = errors.New("agent inbox ring already being placed")

func sessionReadsInboxDoorbells(session *protocol.Session) bool {
	return session != nil && !strings.EqualFold(strings.TrimSpace(session.Agent), protocol.AgentShellValue)
}

type inboxDeliveryState struct {
	mu          sync.Mutex
	timer       *time.Timer
	stopped     bool
	wakeSession protocol.SessionID
}

func (d *Daemon) inboxState(a who.Address) *inboxDeliveryState {
	d.inboxMu.Lock()
	defer d.inboxMu.Unlock()
	if d.inboxStates == nil {
		d.inboxStates = make(map[who.Address]*inboxDeliveryState)
	}
	state := d.inboxStates[a]
	if state == nil {
		state = &inboxDeliveryState{}
		d.inboxStates[a] = state
	}
	return state
}
func (d *Daemon) kickInbox(a who.Address) {
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

type recipient struct {
	ring *protocol.Session
	wake who.MemberKey
	wait string
}
type delivery struct {
	d        *Daemon
	bindings who.Bindings
}

func (r delivery) recipientOf(a who.Address) (recipient, error) {
	return who.SwitchAddress(a, r.toSession, r.toMember, r.toTenderOf, r.toChiefOf)
}
func (r delivery) toSession(id protocol.SessionID) (recipient, error) {
	if s := r.d.store.Get(id); s != nil {
		return recipient{ring: s}, nil
	}
	if r.d.store.DelegationSessionReserved(id) {
		return recipient{wait: fmt.Sprintf("session %s is starting; waits for it to register", shortSessionID(id))}, nil
	}
	if r.d.hubManager != nil && r.d.hubManager.RemoteSession(id) != nil {
		return recipient{wait: fmt.Sprintf("session %s runs on an outpost; remote delivery is unsupported", shortSessionID(id))}, nil
	}
	return recipient{wait: fmt.Sprintf("session %s has ended; waits until it is resumed", shortSessionID(id))}, nil
}
func (r delivery) toMember(k who.MemberKey) (recipient, error) {
	if id, ok := r.bindings.SessionOf(who.Member(k)); ok {
		return r.toSession(id)
	}
	return recipient{wake: k}, nil
}
func (r delivery) toTenderOf(id string) (recipient, error) {
	seed, _, err := r.d.readSeed(id)
	if err != nil {
		return recipient{}, err
	}
	p, ok, err := r.d.seedTender(seed, r.bindings)
	if err != nil {
		return recipient{}, err
	}
	if !ok {
		return recipient{wait: fmt.Sprintf("nobody tends %s; waits for its next tender", id)}, nil
	}
	return who.SwitchParty(p, r.toSession, r.toMember)
}
func (r delivery) toChiefOf(c who.ChiefMailbox) (recipient, error) {
	id := r.d.chiefOfProfile(c.ProfileID)
	if id == "" {
		return recipient{wait: "no Chief session; waits for the next Chief"}, nil
	}
	return r.toSession(id)
}

// inboxWakeRequester names who a wake for this address answers: the oldest unread message's sender.
func (d *Daemon) inboxWakeRequester(a who.Address) string {
	deliveries, err := d.store.UnreadInboxDeliveries(a)
	if err != nil || len(deliveries) == 0 {
		return ""
	}
	if peer := deliveries[0].Peer; peer != nil {
		return d.replyTo(peer.Sender)
	}
	return ""
}
func (d *Daemon) inboxHolder(a who.Address) *protocol.Session {
	b, err := d.bindings()
	if err != nil {
		d.logf("inbox holder: %v", err)
		return nil
	}
	to, err := (delivery{d, b}).recipientOf(a)
	if err != nil {
		d.logf("inbox holder: %v", err)
		return nil
	}
	return to.ring
}
func (d *Daemon) lockInboxState(a who.Address) *inboxDeliveryState {
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
func (d *Daemon) deliverInbox(a who.Address) (inbox.Receipt, error) {
	if d.isRecovering() {
		d.kickInbox(a)
		return inbox.Receipt{Detail: "queued until daemon recovery completes"}, nil
	}
	state := d.lockInboxState(a)
	defer state.mu.Unlock()
	return d.deliverInboxLocked(a, state)
}
func (d *Daemon) deliverInboxLocked(a who.Address, state *inboxDeliveryState) (inbox.Receipt, error) {
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
		receipt.Detail = "queued (daemon is stopping; waits until recovery completes)"
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
		receipt.Detail = "no unread items remain"
		state.wakeSession = ""
		return receipt, nil
	}
	b, err := d.bindings()
	if err != nil {
		return receipt, err
	}
	to, err := (delivery{d, b}).recipientOf(a)
	if err != nil {
		return receipt, err
	}
	holder, memberID := to.ring, to.wake.String()
	finishingWake := holder != nil && holder.ID == state.wakeSession
	if holder == nil || !finishingWake {
		state.wakeSession = ""
	}
	if !finishingWake {
		if to.wait != "" {
			receipt.Detail = "queued (" + to.wait + ")"
			return receipt, nil
		}
		if attempt.Live == 0 {
			receipt.Detail = fmt.Sprintf("queued (inbox.MaxAttempts=%d reached; waits for a read or a new item)", inbox.MaxAttempts)
			return receipt, nil
		}
		now := time.Now()
		due := inbox.Due(attempt.Last, now)
		if due.After(now) {
			d.armInboxLocked(a, state, due.Sub(now))
			receipt.Outstanding = holder != nil && memberID == ""
			receipt.Detail = agentMessageQueuedDetail(errInboxDoorbellOutstanding)
			if memberID != "" && holder == nil {
				if member, found, err := d.resolveCrewMember(memberID); err == nil && found {
					ledger := d.crewWakeLedger()
					ledger.Stamps = parseWakeStamps(member.AutonomousWakes)
					if _, refusal := ledger.Allows(d.memberName(member.Key), now); refusal != nil {
						receipt.Detail = refusal.Error()
					}
				}
			}
			return receipt, nil
		}
		if holder == nil {
			if member := memberID; member != "" {
				key := to.wake
				d.crewWakeMu.Lock()
				result, err := d.crewWakeDayWithChargeLocked(key, "", true, func() error {
					started, err := d.store.StampInboxAttempt(a, now)
					if err != nil {
						return err
					}
					if !started {
						return errInboxNoUnread
					}
					d.logInboxExhaustion(a)
					return nil
				}, crewWakeRequest{RequestedBy: d.inboxWakeRequester(a)})
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
					receipt.Detail = "queued (member is awake; waits for its session to reach a safe prompt)"
					state.wakeSession = ""
					d.kickInbox(a)
					return receipt, nil
				}
				state.wakeSession = result.SessionID
				receipt.Detail = fmt.Sprintf("woke %s in session %s; notification queued until it reaches a safe prompt", d.storedMemberName(member), shortSessionID(result.SessionID))
				return receipt, nil
			}
			receipt.Detail = "queued (" + to.wait + ")"
			return receipt, nil
		}
	}
	if !sessionReadsInboxDoorbells(holder) {
		receipt.Detail = agentMessageQueuedDetail(errInboxNoPromptReader)
		return receipt, nil
	}
	input := maintenanceSessionInput("inbox-ring", uuid.NewString(), holder.ID, inboxRingText, sessionInputWhenPromptReady)
	input.bypassInitialGate = true
	placement := d.sessionInputs().try(context.Background(), input)
	if placement.stage == sessionInputPlaced {
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
func (d *Daemon) logInboxExhaustion(a who.Address) {
	attempt, err := d.store.InboxAttempt(a)
	if err == nil && attempt.Unread > 0 && attempt.Live == 0 {
		d.logf("inbox: stopped ringing %s after %d attempts (inbox.MaxAttempts=%d); %d unread wait for a read or a new item", a, inbox.MaxAttempts, inbox.MaxAttempts, attempt.Unread)
	}
}
func (d *Daemon) armInboxLocked(a who.Address, state *inboxDeliveryState, delay time.Duration) {
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
func (d *Daemon) kickSessionInbox(sessionID protocol.SessionID, state string) {
	if sessionInputPhaseAllows(sessionInputWhenPromptReady, protocol.SessionState(state)) {
		d.kickSessionInboxAddresses(sessionID)
	}
}
func (d *Daemon) kickSeedInboxes() {
	addresses, err := d.store.UnreadInboxAddresses()
	if err != nil {
		d.logf("inbox: seed holder change: %v", err)
		return
	}
	for _, address := range addresses {
		if seedAddressID(address) != "" {
			d.kickInbox(address)
		}
	}
}
func (d *Daemon) kickChiefInboxes() {
	addresses, err := d.store.UnreadInboxAddresses()
	if err != nil {
		d.logf("inbox: chief holder change: %v", err)
		return
	}
	for _, address := range addresses {
		if isChiefAddress(address) {
			d.kickInbox(address)
		}
	}
}
func (d *Daemon) subscribeInboxFacts() {
	d.inboxUnsubscribe = d.eventBus.Subscribe(bus.Filter{FactCrewBound, FactCrewReleased, FactCrewUpdated, FactSessionChiefRoleChanged, FactSessionRegistered, FactSessionUnregistered, FactSessionClosed, FactSessionPTYExited,
		seedEvents.NameTended, seedEvents.NameParked, seedEvents.NameHarvested, seedEvents.NameWithered, seedEvents.NameReplanted}, func(ev bus.Event) {
		switch ev.Name {
		case seedEvents.NameTended, seedEvents.NameParked, seedEvents.NameHarvested, seedEvents.NameWithered, seedEvents.NameReplanted:
			d.kickInbox(who.ToTenderOf(ev.Subject))
		case FactCrewBound, FactCrewReleased, FactCrewUpdated:
			a, err := d.crewFactAddress(ev)
			if err != nil {
				d.logf("inbox crew fact: %v", err)
				return
			}
			d.kickInbox(a)
			d.life.Go("inbox-seed-holder-change", func() { d.kickSeedInboxes() })
		case FactSessionChiefRoleChanged:
			d.life.Go("inbox-chief-change", func() { d.kickChiefInboxes() })
		default:
			// Holder resolution runs outside publishMu, through lifetime work.
			d.life.Go("inbox-holder-change", func() {
				d.kickSessionInboxAddresses(protocol.SessionID(ev.Subject))
				d.kickChiefInboxes()
				d.kickSeedInboxes()
			})
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

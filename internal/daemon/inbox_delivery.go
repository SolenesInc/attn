package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/bus"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func inboxRingText() string { return prompts.RenderText("session", "inbox-notification", nil) }

var errInboxNoUnread = errors.New("agent inbox has no unread items to start a session for")
var errInboxNoPromptReader = errors.New("agent inbox recipient is a shell pane")
var errInboxDoorbellOutstanding = errors.New("agent inbox ring already outstanding")
var errInboxDoorbellInFlight = errors.New("agent inbox ring already being placed")

func sessionReadsInboxDoorbells(session *protocol.Session) bool {
	return session != nil && !strings.EqualFold(strings.TrimSpace(session.Agent), protocol.AgentShellValue)
}

type inboxDeliveryState struct {
	gate           chan struct{}
	timer          *time.Timer
	stopped        bool
	startedSession protocol.SessionID
	tenderResume   *tenderResumeDelivery
	pending        *inboxRing
	rang           protocol.SessionID
}

func (s *inboxDeliveryState) lock()   { s.gate <- struct{}{} }
func (s *inboxDeliveryState) unlock() { <-s.gate }

type inboxRing struct {
	Message            protocol.SessionPlaceMessage `json:"message"`
	FinishingStart     bool                         `json:"finishing_start"`
	BellWaitsForPrompt bool                         `json:"bell_waits_for_prompt"`
	HeldUntil          time.Time                    `json:"held_until"`
	At                 time.Time                    `json:"at"`
}

func (d *Daemon) pendingInboxTargetMatches(pending *inboxRing, session protocol.SessionID) bool {
	if pending.Message.SessionID != session {
		return false
	}
	expected := protocol.Deref(pending.Message.ExpectedDriverRun)
	return expected == "" || (d.store.GetAgentDriverRun(session).RunID == expected && (protocol.Deref(pending.Message.ExpectedConversation) == "" || d.store.GetSessionConversation(session).NativeID == protocol.Deref(pending.Message.ExpectedConversation)))
}

func (d *Daemon) clearPendingInbox(a who.Address, state *inboxDeliveryState) error {
	if state.pending != nil && protocol.Deref(state.pending.Message.ExpectedDriverRun) != "" {
		if err := d.store.ClearPendingInboxInput(a); err != nil {
			return err
		}
	}
	state.pending = nil
	return nil
}

func (d *Daemon) inboxState(a who.Address) *inboxDeliveryState {
	d.inboxMu.Lock()
	defer d.inboxMu.Unlock()
	if d.inboxStates == nil {
		d.inboxStates = make(map[who.Address]*inboxDeliveryState)
	}
	state := d.inboxStates[a]
	if state == nil {
		state = &inboxDeliveryState{gate: make(chan struct{}, 1)}
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
func (d *Daemon) inboxRecipient(a who.Address) (*protocol.Session, string, error) {
	id, _ := a.Session()
	key, _ := a.Member()
	memberID := key.String()
	if seedID, seed := a.TenderOf(); seed {
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			return nil, "", err
		}
		id, _ = seed.Tender.Session()
		if _, member := seed.Tender.Member(); member {
			member, found, err := d.seedTenderMember(seed)
			if err != nil {
				return nil, "", err
			}
			if !found {
				return nil, "", nil
			}
			memberID = member.ID
		}
	}
	if memberID != "" {
		var err error
		id, err = d.crewSessionBoundTo(memberID)
		if err != nil {
			return nil, "", err
		}
	}
	return d.store.Get(id), memberID, nil
}

// inboxStartRequester names who a mail start for this address answers: the oldest unread message's sender.
func (d *Daemon) inboxStartRequester(a who.Address) string {
	deliveries, err := d.store.UnreadInboxDeliveries(a)
	if err != nil || len(deliveries) == 0 {
		return ""
	}
	if deliveries[0].Item.Kind == inbox.QuickCapture {
		return "a quick capture"
	}
	if peer := deliveries[0].Peer; peer != nil {
		return d.peerSenderName(peer.Sender)
	}
	return ""
}
func (d *Daemon) inboxHolder(a who.Address) *protocol.Session {
	holder, _, _ := d.inboxRecipient(a)
	return holder
}
func (d *Daemon) lockInboxState(a who.Address) *inboxDeliveryState {
	for {
		state := d.inboxState(a)
		state.lock()
		d.inboxMu.Lock()
		current := d.inboxStates[a] == state
		d.inboxMu.Unlock()
		if current {
			return state
		}
		state.unlock()
	}
}
func (d *Daemon) deliverInbox(a who.Address) (inbox.Receipt, error) {
	if d.isRecovering() {
		d.kickInbox(a)
		return inbox.Receipt{Detail: "queued until daemon recovery completes"}, nil
	}
	state := d.lockInboxState(a)
	defer state.unlock()
	return d.deliverInboxLocked(a, state)
}
func (d *Daemon) deliverInboxLocked(a who.Address, state *inboxDeliveryState) (inbox.Receipt, error) {
	receipt := inbox.Receipt{}
	defer func() {
		if state.timer != nil || state.startedSession != "" || state.pending != nil {
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
	if state.pending == nil {
		raw, err := d.store.PendingInboxInput(a)
		if err != nil {
			return receipt, err
		}
		if raw != "" {
			var pending inboxRing
			if err := json.Unmarshal([]byte(raw), &pending); err != nil {
				return receipt, err
			}
			state.pending = &pending
		}
	}
	if attempt.Unread == 0 {
		if err := d.clearPendingInbox(a, state); err != nil {
			return receipt, err
		}
		state.startedSession, state.tenderResume = "", nil
		return receipt, nil
	}
	holder, memberID, err := d.inboxRecipient(a)
	if err != nil {
		return receipt, err
	}
	if holder != nil {
		if host := d.unreachableSessionHost(holder.ID); host != "" {
			name := d.outpostName(host)
			receipt.Detail = fmt.Sprintf("queued: %s is on %s, which is unreachable; it rings when %s is back", holder.Label, name, name)
			return receipt, nil
		}
	}
	finishingStart := holder != nil && holder.ID == state.startedSession
	if holder == nil || !finishingStart {
		state.startedSession, state.tenderResume = "", nil
	}
	tenderResume := state.tenderResume
	if finishingStart && tenderResume != nil && tenderResume.claude && !d.claudeModReaches(holder.ID) {
		receipt.Detail = fmt.Sprintf("queued: %s is reopening, and its mail waits for the Claude Code mod's first poll", sessionDisplayName(holder))
		return receipt, nil
	}
	var bellHeldUntil time.Time
	if !finishingStart {
		if attempt.Live == 0 && (holder == nil || attempt.FromUser == 0) {
			return receipt, nil
		}
		now := time.Now()
		due := inbox.Due(attempt.Last, now)
		_, seedMail := a.TenderOf()
		if seedMail && holder == nil && state.rang != "" {
			due, state.rang = now, ""
		}
		nobodyToType := seedMail && holder == nil
		if due.After(now) && attempt.FromUser > 0 && !nobodyToType {
			bellHeldUntil = due
		} else if due.After(now) {
			d.armInboxLocked(a, state, due.Sub(now))
			receipt.Outstanding = holder != nil && memberID == ""
			receipt.Detail = agentMessageQueuedDetail(errInboxDoorbellOutstanding)
			if memberID != "" && holder == nil {
				if member, _, err := d.crewMember(memberID); err == nil {
					ledger := d.crewStartLedger()
					ledger.Stamps = parseStartStamps(member.MailStarts)
					if _, refusal := ledger.Allows(member.Name, now); refusal != nil {
						receipt.Detail = refusal.Error()
					}
				}
			}
			return receipt, nil
		}
		if holder == nil {
			if member := memberID; member != "" {
				if d.reviveMembersInterruptedSession(member) {
					d.kickInbox(a)
					return receipt, nil
				}
				cause, err := d.inboxStartCause(a)
				if err != nil {
					return receipt, err
				}
				seedID, seedMail := a.TenderOf()
				d.crewStartMu.Lock()
				result, err := d.crewStartSessionLocked(member, "", attempt.FromUser == 0 && !seedMail, func() error {
					if seedMail {
						if reopensAt, tripped := d.tenderResumes.reopensAt(seedID, d.clock.Now()); tripped {
							return tenderResumeTripwire{reopensAt: reopensAt}
						}
					}
					started, err := d.store.StampInboxAttempt(a, now)
					if err != nil {
						return err
					}
					if !started {
						return errInboxNoUnread
					}
					d.logInboxExhaustion(a)
					if seedMail {
						count := d.tenderResumes.charge(seedID, d.clock.Now())
						d.unblockTenderResume(seedID)
						d.logf("tender resume: seed %s started %s for %s (resume %d of %d in %s)", seedID, d.crewMemberName(member), d.seedMailCause(a), count, tenderResumeLimit, tenderResumeWindow)
					}
					return nil
				}, crewStartRequest{RequestedBy: d.inboxStartRequester(a), Cause: cause})
				d.crewStartMu.Unlock()
				if errors.Is(err, errInboxNoUnread) {
					return receipt, nil
				}
				if tripwire := (tenderResumeTripwire{}); errors.As(err, &tripwire) {
					seed, _, readErr := d.readSeed(seedID)
					if readErr != nil {
						return receipt, readErr
					}
					receipt.Detail = d.blockTenderResume(a, seed, tripwire.Error())
					d.armInboxLocked(a, state, tripwire.reopensAt.Sub(d.clock.Now()))
					return receipt, nil
				}
				if retired := (errRetiredMember{}); errors.As(err, &retired) || errors.Is(err, errChiefHasNoHarness) {
					state.startedSession = ""
					receipt.Detail = err.Error()
					return receipt, nil
				}
				if err != nil {
					state.startedSession = ""
					d.logf("inbox: mail start for %s refused: %v", a, err)
					receipt.Detail = err.Error()
					d.armInboxLocked(a, state, inbox.AttemptDelay)
					return receipt, nil
				}
				if result.AlreadyInSession {
					state.startedSession = ""
					d.kickInbox(a)
					return receipt, nil
				}
				state.startedSession = result.SessionID
				if seedMail {
					if state.tenderResume, err = d.memberTenderResume(seedID, result.SessionID); err != nil {
						return receipt, err
					}
				}
				receipt.Detail = fmt.Sprintf("started %s in session %s; notification queued until it reaches a safe prompt", d.crewMemberName(member), shortSessionID(result.SessionID))
				return receipt, nil
			}
			if seedID, seed := a.TenderOf(); seed {
				return d.resumeStandbyTenderLocked(a, seedID, state, now)
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
	if state.pending != nil && !d.pendingInboxTargetMatches(state.pending, holder.ID) {
		if err := d.clearPendingInbox(a, state); err != nil {
			return receipt, err
		}
	}
	pending := state.pending
	fresh := pending == nil
	if pending == nil {
		bellWaitsForPrompt := attempt.FromUser > 0 && !sessionmonitor.PromptReady(holder.State, d.stateReason(holder.ID))
		paste, err := d.inboxPaste(a, holder.ProfileID, bellHeldUntil.IsZero() && !bellWaitsForPrompt)
		if err != nil {
			return receipt, err
		}
		if finishingStart && tenderResume != nil {
			paste.text = d.tenderResumeText(tenderResume) + "\n\n" + paste.text
		}
		when := sessionmonitor.WhenWorkingOrPromptReady
		if paste.notified {
			when = sessionmonitor.WhenPromptReady
		}
		input := sessionmonitor.MaintenanceInput(sessionmonitor.InboxRing, uuid.NewString(), paste.text, when)
		input.BypassInitialGate = true
		input.UserWords = len(paste.typed) > 0 && !paste.notified && !(finishingStart && tenderResume != nil)
		at := time.Now()
		input.Ring = &protocol.InboxRingRecord{Address: a.String(), ProfileID: holder.ProfileID, Stamp: store.InboxRingStamp(at), Typed: paste.typed, Notified: paste.notified, Captures: paste.captures, AuthorizedSeeds: paste.authorizedSeeds}
		run := d.store.GetAgentDriverRun(holder.ID)
		if run.Route == "direct" && run.RunID != "" {
			input.ExpectedDriverRun = run.RunID
			input.ExpectedConversation = d.store.GetSessionConversation(holder.ID).NativeID
		}
		pending = &inboxRing{Message: input.Message(holder.ID), FinishingStart: finishingStart, BellWaitsForPrompt: bellWaitsForPrompt, HeldUntil: bellHeldUntil, At: at}
		if input.ExpectedDriverRun != "" {
			raw, err := json.Marshal(pending)
			if err != nil {
				return receipt, err
			}
			if err := d.store.SavePendingInboxInput(a, string(raw)); err != nil {
				return receipt, err
			}
		}
		state.pending = pending
	}
	if protocol.Deref(pending.Message.ExpectedDriverRun) != "" && protocol.Deref(pending.Message.ExpectedConversation) == "" {
		if native := d.store.GetSessionConversation(holder.ID).NativeID; native != "" {
			pending.Message.ExpectedConversation = protocol.Ptr(native)
			raw, err := json.Marshal(pending)
			if err != nil {
				return receipt, err
			}
			if err := d.store.SavePendingInboxInput(a, string(raw)); err != nil {
				return receipt, err
			}
		}
	}
	ring := pending.Message.Ring
	finishingStart, bellWaitsForPrompt, bellHeldUntil := pending.FinishingStart, pending.BellWaitsForPrompt, pending.HeldUntil
	placement := d.placeDelivery(context.Background(), holder.ID, sessionmonitor.DeliveryFromMessage(pending.Message))
	if placement.Stage == sessionmonitor.Placed {
		readAt, err := d.store.RingInbox(a, finishingStart, ring.Typed, holder.ID, ring.Notified, pending.At)
		pausepoint.Done(pausepoint.InboxRingRecorded, err)
		if err != nil {
			return receipt, err
		}
		state.pending = nil
		state.tenderResume = nil
		state.rang = holder.ID
		for _, captureID := range ring.Captures {
			d.publishQuickCaptureRead(holder.ProfileID, captureID, readAt)
		}
		for _, seedID := range ring.AuthorizedSeeds {
			d.publishFact(FactSeedAuthorizationTyped, seedID, map[string]string{"profile_id": holder.ProfileID, "read_at": readAt})
		}
		if !placement.Held {
			d.endStandbyOnceMailRead(a)
		}
		if !bellWaitsForPrompt {
			state.startedSession = ""
		}
		if !finishingStart {
			d.logInboxExhaustion(a)
		}
		if ring.Notified {
			d.armInboxLocked(a, state, inbox.AttemptDelay)
		} else if !bellHeldUntil.IsZero() {
			d.armInboxLocked(a, state, time.Until(bellHeldUntil))
		}
		if remaining, err := d.store.InboxAttempt(a); err == nil && remaining.FromUser > 0 {
			d.kickInbox(a)
		}
		receipt.Rang = true
		receipt.Detail = "notified " + sessionDisplayName(holder)
		return receipt, nil
	}
	var callError *placementCallError
	if placement.Stage == sessionmonitor.Failed || (!errors.As(placement.Err, &callError) && !placement.Uncertain && (fresh || protocol.Deref(pending.Message.ExpectedDriverRun) == "")) {
		if err := d.clearPendingInbox(a, state); err != nil {
			return receipt, err
		}
	}
	receipt.Detail = agentMessageQueuedDetail(placement.Err)
	delay, retry := sessionmonitor.RetryDelay(placement.Err)
	if retry {
		d.armInboxLocked(a, state, delay)
	}
	return receipt, nil
}

type inboxRingReturned struct {
	ProfileID       string   `json:"profile_id"`
	Captures        []string `json:"captures,omitempty"`
	AuthorizedSeeds []string `json:"authorized_seeds,omitempty"`
}

func (d *Daemon) settleInboxRing(_ context.Context, msg protocol.InboxRingSettleMessage) (struct{}, error) {
	address, err := d.heldRingAddress(msg.Ring.Address)
	if err != nil {
		return struct{}{}, fmt.Errorf("inbox ring %s: %w", msg.DeliveryID, err)
	}
	state := d.lockInboxState(address)
	defer state.unlock()
	if msg.Outcome == protocol.InboxRingOutcomePlaced {
		d.endStandbyOnceMailRead(address)
		return struct{}{}, nil
	}
	if state.pending != nil && state.pending.Message.DeliveryID == msg.DeliveryID {
		if err := d.clearPendingInbox(address, state); err != nil {
			return struct{}{}, err
		}
	}
	if err := d.store.ReturnInboxRing(address, msg.Ring.Typed, msg.Ring.Notified, msg.Ring.Stamp); err != nil {
		return struct{}{}, fmt.Errorf("return ring to %s: %w", address, err)
	}
	d.publishFact(FactInboxRingReturned, address.String(), inboxRingReturned{ProfileID: msg.Ring.ProfileID, Captures: msg.Ring.Captures, AuthorizedSeeds: msg.Ring.AuthorizedSeeds})
	d.kickInbox(address)
	return struct{}{}, nil
}

func (d *Daemon) heldRingAddress(recorded string) (who.Address, error) {
	if profileID, heldSinceBeforeChiefMembers := strings.CutPrefix(recorded, "chief:"); heldSinceBeforeChiefMembers {
		return d.migratedChiefMailbox(profileID)
	}
	return who.ParseAddress(recorded)
}

func (d *Daemon) migratedChiefMailbox(profileID string) (who.Address, error) {
	chiefs, err := d.store.ProfileChiefMembers()
	if err != nil {
		return who.Address{}, err
	}
	key, err := who.ParseMemberKey(chiefs[profileID])
	if err != nil {
		return who.Address{}, fmt.Errorf("profile %s has no Chief member: %w", profileID, err)
	}
	return who.Member(key).Address(), nil
}

func (d *Daemon) projectReturnedRing(ev bus.Event) {
	returned, ok := decodeFact[inboxRingReturned](d, ev)
	if !ok {
		return
	}
	if len(returned.AuthorizedSeeds) > 0 {
		d.projectGardenSeeds()
	}
	for _, captureID := range returned.Captures {
		record, err := d.store.QuickCapture(returned.ProfileID, captureID)
		if err != nil {
			d.logf("returned ring projection: quick capture %s: %v", captureID, err)
			continue
		}
		if record.ReadAt != nil {
			continue
		}
		message := protocol.QuickCaptureUnreadMessage{ProfileID: returned.ProfileID, Event: protocol.EventQuickCaptureUnread, CaptureID: captureID}
		d.wsHub.ForEachClient(func(client *wsClient) {
			if client.isTrustedAppClient() && client.selectedProfile() == returned.ProfileID {
				d.sendToClient(client, message)
			}
		})
	}
}

type inboxPaste struct {
	text            string
	typed           []string
	captures        []string
	authorizedSeeds []string
	notified        bool
}

func (d *Daemon) inboxPaste(a who.Address, profileID string, bell bool) (inboxPaste, error) {
	deliveries, err := d.store.UnreadInboxDeliveries(a)
	if err != nil {
		return inboxPaste{}, err
	}
	var paste inboxPaste
	var texts []string
	for _, delivery := range deliveries {
		item := delivery.Item
		if !item.Kind.Typed() {
			paste.notified = bell
			continue
		}
		if item.Kind == inbox.QuickCapture {
			record, err := d.store.QuickCapture(profileID, item.Source)
			if err != nil {
				return inboxPaste{}, fmt.Errorf("quick capture %s: %w", item.Source, err)
			}
			texts = append(texts, inbox.QuickCaptureText(item.Text, item.Source, record.Attachments))
			paste.captures = append(paste.captures, item.Source)
		} else {
			texts = append(texts, item.Text)
		}
		if item.Kind == inbox.Authorization {
			paste.authorizedSeeds = append(paste.authorizedSeeds, item.Source)
		}
		paste.typed = append(paste.typed, item.ID)
	}
	if paste.notified {
		texts = append(texts, inboxRingText())
	}
	paste.text = strings.Join(texts, "\n\n")
	return paste, nil
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
		state.lock()
		defer state.unlock()
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
	if sessionmonitor.PhaseAllows(sessionmonitor.WhenWorkingOrPromptReady, protocol.SessionState(state)) {
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
		if _, seed := address.TenderOf(); seed {
			d.kickInbox(address)
		}
	}
}
func (d *Daemon) subscribeInboxFacts() {
	d.inboxUnsubscribe = d.eventBus.Subscribe(bus.Filter{FactCrewBound, FactCrewReleased, FactCrewUpdated, FactSessionRegistered, FactSessionUnregistered, FactSessionClosed, FactSessionPTYExited,
		seedEvents.NameTended, seedEvents.NameParked, seedEvents.NameHarvested, seedEvents.NameWithered, seedEvents.NameReplanted}, func(ev bus.Event) {
		switch ev.Name {
		case seedEvents.NameTended, seedEvents.NameParked, seedEvents.NameHarvested, seedEvents.NameWithered, seedEvents.NameReplanted:
			d.kickInbox(who.ToTenderOf(ev.Subject))
		case FactCrewBound, FactCrewReleased, FactCrewUpdated:
			if address, err := memberAddress(ev.Subject); err != nil {
				d.logf("inbox: crew fact for %q: %v", ev.Subject, err)
			} else {
				d.kickInbox(address)
			}
			d.life.Go("inbox-seed-holder-change", func() { d.kickSeedInboxes() })
		default:
			// Holder resolution runs outside publishMu, through lifetime work.
			d.life.Go("inbox-holder-change", func() {
				d.kickSessionInboxAddresses(protocol.SessionID(ev.Subject))
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
		state.lock()
		state.stopped = true
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
		state.unlock()
	}
}

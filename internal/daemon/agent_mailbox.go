package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/protocol"
)

const agentMailboxDoorbellText = "📬 You have unread items in your attn inbox. Run attn agent inbox to read them."

var (
	errAgentMailboxDoorbellOutstanding = errors.New("agent inbox doorbell already outstanding")
	errAgentMailboxDoorbellInFlight    = errors.New("agent inbox doorbell delivery already in flight")
	errAgentMailboxRecipientGone       = errors.New("agent inbox recipient is gone")
)

type agentMailboxDoorbellState struct {
	unread      bool
	outstanding bool
	delivering  bool
	lastSentAt  time.Time
	retry       *time.Timer
}

func (d *Daemon) agentMailboxCooldown() time.Duration {
	if d.agentMailboxCooldownOverride > 0 {
		return d.agentMailboxCooldownOverride
	}
	return sessionInputQuietWindow
}

// Producers persist first, then call this adapter. The terminal only carries a
// generic doorbell; reading the durable inbox is the content receipt.
func (d *Daemon) deliverAgentMailboxItem(delivery agentmailbox.Delivery) error {
	recipient := delivery.Item.RecipientSessionID
	unread, err := d.store.HasUnreadAgentMailboxItems(recipient)
	if err != nil {
		return err
	}
	if !unread {
		d.refreshAgentMailboxUnread(recipient)
		return nil
	}
	d.noteQueuedAgentMailboxItem(recipient)
	return d.deliverAgentMailboxDoorbell(recipient)
}

func (d *Daemon) deliverAgentMailboxDoorbell(sessionID string) error {
	if d.store.Get(sessionID) == nil {
		d.forgetAgentMailboxDoorbell(sessionID)
		return fmt.Errorf("%w: %s", errAgentMailboxRecipientGone, sessionID)
	}

	d.agentMailboxMu.Lock()
	state := d.agentMailboxDoorbells[sessionID]
	switch {
	case state == nil || !state.unread:
		d.agentMailboxMu.Unlock()
		return nil
	case state.outstanding:
		d.agentMailboxMu.Unlock()
		return errAgentMailboxDoorbellOutstanding
	case state.delivering:
		d.agentMailboxMu.Unlock()
		return errAgentMailboxDoorbellInFlight
	}
	state.delivering = true
	if state.retry != nil {
		state.retry.Stop()
		state.retry = nil
	}
	d.agentMailboxMu.Unlock()

	attemptKey := uuid.NewString()
	id := inputAttemptID("agent-mailbox-doorbell", attemptKey)
	input := maintenanceSessionInput(
		"agent-mailbox-doorbell",
		attemptKey,
		sessionID,
		agentMailboxDoorbellText,
		sessionInputWhenPromptReady,
	)
	input.bypassInitialGate = true
	attempt := d.sessionInputs().try(context.Background(), input)
	succeeded := attempt.err == nil && (attempt.stage == sessionInputPlaced || attempt.stage == sessionInputTaken)
	if succeeded {
		// A successful paste plus Enter completes a doorbell attempt. Keeping this
		// composer until a hook arrives would make a missing hook block the lane.
		d.sessionInputs().forget(sessionID, id)
		if _, err := d.store.MarkAgentMailboxNotified(sessionID, time.Now()); err != nil {
			d.logf("agent inbox doorbell placed but unread rows were not stamped: session=%s err=%v", sessionID, err)
		}
	}

	d.agentMailboxMu.Lock()
	current := d.agentMailboxDoorbells[sessionID]
	if current != state {
		d.agentMailboxMu.Unlock()
		return attempt.err
	}
	unread, err := d.store.HasUnreadAgentMailboxItems(sessionID)
	if err != nil {
		d.logf("agent inbox unread check: session=%s err=%v", sessionID, err)
		unread = true
	}
	state.delivering = false
	state.unread = unread
	if !unread {
		delete(d.agentMailboxDoorbells, sessionID)
	} else if d.store.Get(sessionID) == nil {
		delete(d.agentMailboxDoorbells, sessionID)
	} else {
		after := d.agentMailboxCooldown()
		if succeeded {
			state.outstanding = true
			state.lastSentAt = time.Now()
		} else {
			state.outstanding = false
			if retryAfter, retryable := sessionInputRetryDelay(attempt.err); retryable && retryAfter > 0 {
				after = retryAfter
			}
		}
		d.armAgentMailboxDoorbellLocked(sessionID, state, after)
	}
	d.agentMailboxMu.Unlock()
	return attempt.err
}

func (d *Daemon) armAgentMailboxDoorbellLocked(sessionID string, state *agentMailboxDoorbellState, after time.Duration) {
	if state.retry != nil {
		state.retry.Stop()
	}
	if after <= 0 {
		after = d.agentMailboxCooldown()
	}
	var timer *time.Timer
	timer = time.AfterFunc(after, func() {
		select {
		case <-d.done:
			return
		default:
		}
		d.agentMailboxMu.Lock()
		current := d.agentMailboxDoorbells[sessionID]
		if current != state || state.retry != timer || state.delivering || !state.unread {
			d.agentMailboxMu.Unlock()
			return
		}
		state.retry = nil
		state.outstanding = false
		d.agentMailboxMu.Unlock()
		d.drainQueuedAgentMailboxItems(sessionID)
	})
	state.retry = timer
}

func (d *Daemon) notePostInitialPrompt(sessionID string) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	if d.postInitialPrompt == nil {
		d.postInitialPrompt = make(map[string]struct{})
	}
	d.postInitialPrompt[sessionID] = struct{}{}
}

func (d *Daemon) forgetPostInitialPrompt(sessionID string) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	delete(d.postInitialPrompt, sessionID)
}

func (d *Daemon) initialPromptPending(sessionID string) bool {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	_, pending := d.postInitialPrompt[sessionID]
	return pending
}

func (d *Daemon) runPostInitialPrompt(sessionID, state string) {
	if state != protocol.StateWorking {
		return
	}
	d.agentMailboxMu.Lock()
	_, pending := d.postInitialPrompt[sessionID]
	delete(d.postInitialPrompt, sessionID)
	d.agentMailboxMu.Unlock()
	if pending {
		d.drainAgentMailboxAfterStateChange(sessionID, state)
	}
}

func (d *Daemon) rollbackQueuedPeerMessage(sessionID, messageID string) {
	if err := d.store.DeleteQueuedPeerMessage(messageID); err != nil {
		d.logf("agent msg rollback: session=%s id=%s err=%v", sessionID, messageID, err)
	}
	d.refreshAgentMailboxUnread(sessionID)
}

// This cache keeps per-session state observations off SQLite's hot path.
func (d *Daemon) noteQueuedAgentMailboxItem(sessionID string) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	if d.agentMailboxDoorbells == nil {
		d.agentMailboxDoorbells = make(map[string]*agentMailboxDoorbellState)
	}
	state := d.agentMailboxDoorbells[sessionID]
	if state == nil {
		state = &agentMailboxDoorbellState{}
		d.agentMailboxDoorbells[sessionID] = state
	}
	state.unread = true
}

func (d *Daemon) hasQueuedAgentMailboxItems(sessionID string) bool {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	state := d.agentMailboxDoorbells[sessionID]
	return state != nil && state.unread
}

// Unread rows outlive the daemon. Rebuild the in-memory doorbell state and
// attempt a wake so a restart cannot strand inbox content.
func (d *Daemon) seedQueuedAgentMailboxItems() {
	if d.store == nil {
		return
	}
	recipients, err := d.store.TargetsWithUnreadAgentMailboxItems()
	if err != nil {
		d.logf("agent mailbox seed: %v", err)
		return
	}
	for _, recipient := range recipients {
		select {
		case <-d.done:
			return
		default:
		}
		d.noteQueuedAgentMailboxItem(recipient)
		d.drainQueuedAgentMailboxItems(recipient)
	}
}

func (d *Daemon) drainAgentMailboxAfterStateChange(sessionID, state string) {
	if !sessionInputPhaseAllows(sessionInputWhenPromptReady, protocol.SessionState(state)) ||
		!d.hasQueuedAgentMailboxItems(sessionID) {
		return
	}
	if d.agentMailboxDrainScheduledHook != nil {
		d.agentMailboxDrainScheduledHook(sessionID)
	}
	go d.drainQueuedAgentMailboxItems(sessionID)
}

func (d *Daemon) drainQueuedAgentMailboxItems(sessionID string) {
	err := d.deliverAgentMailboxDoorbell(sessionID)
	delivered := 0
	if err == nil {
		delivered = 1
	} else if !errors.Is(err, errAgentMailboxDoorbellOutstanding) && !errors.Is(err, errAgentMailboxDoorbellInFlight) {
		d.logf("agent inbox doorbell deferred: session=%s err=%v", sessionID, err)
	}
	if d.agentMailboxDrainHook != nil {
		d.agentMailboxDrainHook(sessionID, delivered)
	}
}

func (d *Daemon) noteAgentMailboxRead(sessionID string, remaining int) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	unread := remaining > 0
	if d.store != nil {
		latest, err := d.store.HasUnreadAgentMailboxItems(sessionID)
		if err != nil {
			d.logf("agent inbox unread receipt check: session=%s err=%v", sessionID, err)
		} else {
			unread = latest
		}
	}
	state := d.agentMailboxDoorbells[sessionID]
	if !unread {
		if state != nil && state.retry != nil {
			state.retry.Stop()
		}
		delete(d.agentMailboxDoorbells, sessionID)
		return
	}
	if state == nil {
		state = &agentMailboxDoorbellState{}
		if d.agentMailboxDoorbells == nil {
			d.agentMailboxDoorbells = make(map[string]*agentMailboxDoorbellState)
		}
		d.agentMailboxDoorbells[sessionID] = state
	}
	state.unread = true
	state.outstanding = true
	if state.delivering {
		return
	}
	after := d.agentMailboxCooldown()
	if elapsed := time.Since(state.lastSentAt); !state.lastSentAt.IsZero() && elapsed < after {
		after -= elapsed
	}
	d.armAgentMailboxDoorbellLocked(sessionID, state, after)
}

func (d *Daemon) refreshAgentMailboxUnread(sessionID string) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	unread, err := d.store.HasUnreadAgentMailboxItems(sessionID)
	if err != nil {
		d.logf("agent inbox unread refresh: session=%s err=%v", sessionID, err)
		return
	}
	if unread {
		state := d.agentMailboxDoorbells[sessionID]
		if state == nil {
			state = &agentMailboxDoorbellState{}
			if d.agentMailboxDoorbells == nil {
				d.agentMailboxDoorbells = make(map[string]*agentMailboxDoorbellState)
			}
			d.agentMailboxDoorbells[sessionID] = state
		}
		state.unread = true
		return
	}
	if state := d.agentMailboxDoorbells[sessionID]; state != nil && state.retry != nil {
		state.retry.Stop()
	}
	delete(d.agentMailboxDoorbells, sessionID)
}

func (d *Daemon) forgetAgentMailboxDoorbell(sessionID string) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	if state := d.agentMailboxDoorbells[sessionID]; state != nil && state.retry != nil {
		state.retry.Stop()
	}
	delete(d.agentMailboxDoorbells, sessionID)
}

func (d *Daemon) stopAgentMailboxDoorbells() {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	for sessionID, state := range d.agentMailboxDoorbells {
		if state.retry != nil {
			state.retry.Stop()
		}
		delete(d.agentMailboxDoorbells, sessionID)
	}
}

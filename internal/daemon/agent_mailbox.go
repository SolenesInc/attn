package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/protocol"
)

type agentMailboxDeliveryFlight struct {
	done chan struct{}
	err  error
}

func (d *Daemon) deliverAgentMailboxItem(delivery agentmailbox.Delivery) error {
	d.agentMailboxMu.Lock()
	if d.agentMailboxDeliveries == nil {
		d.agentMailboxDeliveries = make(map[string]*agentMailboxDeliveryFlight)
	}
	if flight := d.agentMailboxDeliveries[delivery.Item.ID]; flight != nil {
		d.agentMailboxMu.Unlock()
		<-flight.done
		return flight.err
	}
	flight := &agentMailboxDeliveryFlight{done: make(chan struct{})}
	d.agentMailboxDeliveries[delivery.Item.ID] = flight
	d.agentMailboxMu.Unlock()

	err := d.deliverAgentMailboxItemOnce(delivery)
	d.agentMailboxMu.Lock()
	flight.err = err
	delete(d.agentMailboxDeliveries, delivery.Item.ID)
	close(flight.done)
	d.agentMailboxMu.Unlock()
	return err
}

func (d *Daemon) deliverAgentMailboxItemOnce(delivery agentmailbox.Delivery) error {
	item := delivery.Item
	queued, err := d.store.AgentMailboxItemQueued(item.ID)
	if err != nil {
		return err
	}
	if !queued {
		return nil
	}
	if d.initialPromptPending(item.RecipientSessionID) {
		return errAgentMessageInitialPromptPending
	}
	text, senderSessionID, err := d.composeAgentMailboxPrompt(delivery)
	if err != nil {
		return err
	}
	id := inputAttemptID("agent-mailbox", item.ID)
	input := peerAgentSessionInput(item.ID, senderSessionID, item.RecipientSessionID, text)
	input.id = id
	attempt := d.sessionInputs().try(context.Background(), input)
	if attempt.err != nil {
		return attempt.err
	}
	if item.Kind == agentmailbox.KindGardenSeed && (attempt.stage == sessionInputPlaced || attempt.stage == sessionInputTaken) {
		// Seed show is the read receipt. Successful placement only releases the
		// input lane so every harness follows the same Garden protocol.
		d.sessionInputs().forget(item.RecipientSessionID, id)
		return d.stampAgentMailboxItemNotified(item.RecipientSessionID, item.ID)
	}
	if d.sessionRunsWhatIsTyped(item.RecipientSessionID) && attempt.stage == sessionInputPlaced {
		// A shell starts no turn to take the words with, so placement is the
		// receipt; waiting for one leaves the lane blocked against every later message.
		d.sessionInputs().forget(item.RecipientSessionID, id)
		return d.stampAgentMailboxItemHandled(item.RecipientSessionID, item.ID)
	}
	if sessionInputTakenWindow > 0 && attempt.stage == sessionInputPlaced {
		attempt = d.sessionInputs().await(item.RecipientSessionID, id, attempt.wait, sessionInputTakenWindow)
		if attempt.stage != sessionInputTaken {
			attempt = d.sessionInputs().try(context.Background(), input)
			if attempt.err != nil {
				return attempt.err
			}
			attempt = d.sessionInputs().await(item.RecipientSessionID, id, attempt.wait, sessionInputTakenWindow)
		}
	}
	if sessionInputTakenWindow > 0 && attempt.stage != sessionInputTaken {
		return errDoorbellNotTaken
	}
	return d.stampAgentMailboxItemHandled(item.RecipientSessionID, item.ID)
}

func (d *Daemon) sessionRunsWhatIsTyped(sessionID string) bool {
	session := d.store.Get(sessionID)
	return session != nil && string(session.Agent) == protocol.AgentShellValue
}

func (d *Daemon) stampAgentMailboxItemNotified(sessionID, id string) error {
	d.sessionInputs().release(sessionID, inputAttemptID("agent-mailbox", id))
	if err := d.store.MarkAgentMailboxItemNotified(id, time.Now()); err != nil {
		// The words already landed; failing to stamp would redeliver them, which
		// is worse than losing the receipt.
		d.logf("agent mailbox item notified but not stamped: id=%s err=%v", id, err)
	}
	return nil
}

func (d *Daemon) stampAgentMailboxItemHandled(sessionID, id string) error {
	d.sessionInputs().release(sessionID, inputAttemptID("agent-mailbox", id))
	if err := d.store.MarkAgentMailboxItemHandled(id, time.Now()); err != nil {
		d.logf("agent mailbox item handled but not stamped: id=%s err=%v", id, err)
	}
	return nil
}

func (d *Daemon) noteInitialAgentMessage(sessionID, messageID string) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	if d.agentMessageInitialPrompt == nil {
		d.agentMessageInitialPrompt = make(map[string]string)
	}
	d.agentMessageInitialPrompt[sessionID] = messageID
}

func (d *Daemon) initialAgentMessagePending(sessionID, messageID string) bool {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	current := d.agentMessageInitialPrompt[sessionID]
	return current != "" && (messageID == "" || current == messageID)
}

func (d *Daemon) notePostInitialPrompt(sessionID string, after func()) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	if d.postInitialPrompt == nil {
		d.postInitialPrompt = make(map[string]func())
	}
	d.postInitialPrompt[sessionID] = after
}

func (d *Daemon) forgetPostInitialPrompt(sessionID string) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	delete(d.postInitialPrompt, sessionID)
}

func (d *Daemon) initialPromptPending(sessionID string) bool {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	_, postPending := d.postInitialPrompt[sessionID]
	return d.agentMessageInitialPrompt[sessionID] != "" || postPending
}

func (d *Daemon) runPostInitialPrompt(sessionID, state string) {
	if state != protocol.StateWorking {
		return
	}
	d.agentMailboxMu.Lock()
	after, pending := d.postInitialPrompt[sessionID]
	delete(d.postInitialPrompt, sessionID)
	d.agentMailboxMu.Unlock()
	if pending {
		if after != nil {
			after()
		}
		d.drainAgentMailboxAfterStateChange(sessionID, state)
	}
}

// Worker state is not enough: a freshly spawned Claude session reports `working`
// while still at its trust dialog, and hook evidence lands past that dialog.
func (d *Daemon) noteInitialAgentMessageSubmitted(sessionID, state string) {
	if state != protocol.StateWorking {
		return
	}
	d.agentMailboxMu.Lock()
	messageID := d.agentMessageInitialPrompt[sessionID]
	delete(d.agentMessageInitialPrompt, sessionID)
	d.agentMailboxMu.Unlock()
	if messageID == "" {
		return
	}
	_ = d.stampAgentMailboxItemHandled(sessionID, messageID)
	d.drainAgentMailboxAfterStateChange(sessionID, state)
}

func (d *Daemon) rollbackInitialAgentMessage(sessionID, messageID string) {
	d.agentMailboxMu.Lock()
	if d.agentMessageInitialPrompt[sessionID] == messageID {
		delete(d.agentMessageInitialPrompt, sessionID)
	}
	d.agentMailboxMu.Unlock()
	if err := d.store.DeleteQueuedPeerMessage(messageID); err != nil {
		d.logf("agent msg rollback: session=%s id=%s err=%v", sessionID, messageID, err)
	}
	d.forgetQueuedAgentMailboxItems(sessionID)
}

func (d *Daemon) composeAgentMailboxPrompt(delivery agentmailbox.Delivery) (string, string, error) {
	switch delivery.Item.Kind {
	case agentmailbox.KindGardenSeed:
		return fmt.Sprintf("🔔 %s moved: %s — read it with `attn seed show %s`.",
			delivery.Item.SourceID, delivery.Item.Hint, delivery.Item.SourceID), "", nil
	case agentmailbox.KindMaintenancePrompt:
		return delivery.Item.Prompt, "", nil
	case agentmailbox.KindPeerMessage:
		if delivery.Peer == nil {
			return "", "", fmt.Errorf("peer mailbox item %s has no message", delivery.Item.ID)
		}
		sender := d.store.Get(delivery.Peer.SenderSessionID)
		return d.composeAgentMessage(sender, *delivery.Peer), delivery.Peer.SenderSessionID, nil
	default:
		return "", "", fmt.Errorf("agent mailbox item %s has unknown kind %q", delivery.Item.ID, delivery.Item.Kind)
	}
}

// noteQueuedAgentMailboxItem keeps the state-change drain a map lookup: state reports
// arrive about once a second per session, and a DB query each time is idle burn.
func (d *Daemon) noteQueuedAgentMailboxItem(recipientSessionID string) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	if d.queuedAgentMailboxItems == nil {
		d.queuedAgentMailboxItems = make(map[string]bool)
	}
	d.queuedAgentMailboxItems[recipientSessionID] = true
}

func (d *Daemon) hasQueuedAgentMailboxItems(recipientSessionID string) bool {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	return d.queuedAgentMailboxItems[recipientSessionID]
}

// seedQueuedAgentMailboxItems restores the drain's memory across a daemon restart:
// rows outlive the process, and a message nobody remembers is queued forever.
func (d *Daemon) seedQueuedAgentMailboxItems() {
	if d.store == nil {
		return
	}
	recipients, err := d.store.TargetsWithQueuedAgentMailboxItems()
	if err != nil {
		d.logf("agent mailbox seed: %v", err)
		return
	}
	for _, recipient := range recipients {
		d.noteQueuedAgentMailboxItem(recipient)
	}
}

// drainAgentMailboxAfterStateChange is the retry rail. Nothing else re-arms a
// blocked delivery, so a message to a session awaiting approval would sit queued.
func (d *Daemon) drainAgentMailboxAfterStateChange(sessionID, state string) {
	if d.initialPromptPending(sessionID) || !sessionInputPhaseAllows(sessionInputAtTurnBoundary, protocol.SessionState(state)) || !d.hasQueuedAgentMailboxItems(sessionID) {
		return
	}
	if d.agentMailboxDrainScheduledHook != nil {
		d.agentMailboxDrainScheduledHook(sessionID)
	}
	go d.drainQueuedAgentMailboxItems(sessionID)
}

func (d *Daemon) drainQueuedAgentMailboxItems(sessionID string) {
	if !d.beginAgentMailboxDrain(sessionID) {
		return
	}
	defer d.endAgentMailboxDrain(sessionID)

	queued, err := d.store.QueuedAgentMailboxDeliveries(sessionID)
	if err != nil {
		d.logf("agent mailbox drain: session=%s err=%v", sessionID, err)
		return
	}
	delivered := 0
	for _, delivery := range queued {
		if err := d.deliverAgentMailboxItem(delivery); err != nil {
			d.logf("agent mailbox drain stopped: session=%s id=%s err=%v", sessionID, delivery.Item.ID, err)
			break
		}
		delivered++
	}
	if delivered == len(queued) {
		d.forgetQueuedAgentMailboxItems(sessionID)
	}
	if d.agentMailboxDrainHook != nil {
		d.agentMailboxDrainHook(sessionID, delivered)
	}
}

func (d *Daemon) beginAgentMailboxDrain(sessionID string) bool {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	if d.drainingAgentMailbox == nil {
		d.drainingAgentMailbox = make(map[string]bool)
	}
	if d.drainingAgentMailbox[sessionID] {
		return false
	}
	d.drainingAgentMailbox[sessionID] = true
	return true
}

func (d *Daemon) endAgentMailboxDrain(sessionID string) {
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	delete(d.drainingAgentMailbox, sessionID)
}

// forgetQueuedAgentMailboxItems clears the flag only when the store agrees the
// queue is empty, so a message enqueued mid-drain still has a drain to wake.
func (d *Daemon) forgetQueuedAgentMailboxItems(sessionID string) {
	remaining, err := d.store.QueuedAgentMailboxDeliveries(sessionID)
	if err != nil || len(remaining) > 0 {
		return
	}
	d.agentMailboxMu.Lock()
	defer d.agentMailboxMu.Unlock()
	delete(d.queuedAgentMailboxItems, sessionID)
}

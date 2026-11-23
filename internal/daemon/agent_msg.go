package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/who"
)

const (
	agentMessageDedupeWindow = 10 * time.Second
	agentMessageRateWindow   = 30 * time.Second
	agentMessageRateLimit    = 8
	agentMessageQueueCap     = 50
)

func agentMessageGuardVerdict(counts inbox.PeerGuardCounts) string {
	switch {
	case counts.DuplicateFromSender:
		return fmt.Sprintf(
			"you already sent that exact text to this session within the last %s; say something new, or wait for a reply",
			agentMessageDedupeWindow)
	case counts.FromSenderInWindow >= agentMessageRateLimit:
		return fmt.Sprintf(
			"rate limit: %d messages per %s to one session, and you have sent %d; slow down",
			agentMessageRateLimit, agentMessageRateWindow, counts.FromSenderInWindow)
	case counts.UnreadForRecipient >= agentMessageQueueCap:
		return fmt.Sprintf(
			"that session has %d unread messages and the queue cap is %d; it has to read some before more arrive",
			counts.UnreadForRecipient, agentMessageQueueCap)
	}
	return ""
}

func (d *Daemon) runAgentMsg(ctx context.Context, c command.Caller, a command.AgentMsgArgs) (any, error) {
	if c.Session == "" {
		return nil, command.Refuse(command.CodeNoSender, "no sender: this shell is not an attn session, so pass --source-session <id> (`{attn agent list}` names the sessions)")
	}
	sender := d.store.Get(c.Session)
	if sender == nil {
		return nil, senderRefusal(string(c.Session), "sender")
	}
	msg := agentMsgTarget(a.Target)
	switch {
	case a.Steer && a.Interrupt:
		return nil, command.Refuse(command.CodeSteerAndInterrupt, "--steer goes into the running turn and --interrupt stops it; pass one")
	case a.Steer && a.AsUser:
		return nil, command.Refuse(command.CodeAsUserWithSteer, "a steer reaches the recipient as a note from attn, never as the user's words")
	}

	content := strings.TrimSpace(a.Text)
	result := &protocol.AgentMsgResult{Status: protocol.AgentMsgStatusRefused}
	switch {
	case content == "":
		result.Detail = "the message is empty; there is nothing to deliver"
	case len(content) > protocol.AgentMessageMaxChars:
		result.Detail = fmt.Sprintf(
			"message is %d bytes and the limit is %d; send the gist and point at the rest",
			len(content), protocol.AgentMessageMaxChars)
	}
	if result.Detail != "" {
		return result, nil
	}

	targetRef := protocol.SessionID(msg.TargetSessionID)
	var address who.Address
	if seedID := strings.TrimSpace(protocol.Deref(msg.TargetSeedID)); seedID != "" {
		if err := d.requireSeedInProfile(seedID, sender.ProfileID, false); err != nil {
			return nil, command.Refuse("cross_profile", "%s", err.Error())
		}
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			return nil, command.Refuse("seed_not_found", "%s", err.Error())
		}
		address = who.ToTenderOf(seed.ID)
	} else {
		// A target in another profile is answered exactly like an unknown one.
		notFound := func() error {
			return command.Refuse("session_or_crew_member_not_found", "no session or crew member matches %q; `{attn agent list}` names sessions and `{attn crew list}` names members", protocol.TrimID(targetRef))
		}
		member, found, memberErr := d.crewMemberIn(sender.ProfileID, string(targetRef))
		if found {
			address = who.Member(member.Key).Address()
		} else {
			target, code := d.resolveSessionByIDOrPrefix(string(targetRef))
			if target == nil {
				if memberErr != nil {
					return nil, memberErr
				}
				if code == "session_not_found" {
					return nil, notFound()
				}
				return nil, command.Refuse(code, "%q matches more than one session; give more of the id (`{attn agent list --json}` carries full ids)", protocol.TrimID(targetRef))
			}
			if target.ProfileID != sender.ProfileID {
				return nil, notFound()
			}
			address = d.inboxAddressOf(target.ID)
		}
	}
	if key, _ := address.Member(); !key.IsZero() {
		if retired, ok := d.retiredCrewMember(key.String()); ok {
			result.Detail = retired.Name + " is retired"
			return result, nil
		}
	}
	target := d.inboxHolder(address)
	if target != nil {
		result.TargetSessionID = target.ID
		if sender.ID == target.ID {
			result.Detail = "that is this session; a message to yourself is not a conversation"
			return result, nil
		}
	}
	now := time.Now()
	senderParty := d.partyOfSession(sender.ID)
	counts, err := d.store.PeerMessageGuardCounts(senderParty, address, content, now.Add(-agentMessageDedupeWindow), now.Add(-agentMessageRateWindow))
	if err != nil {
		d.logf("agent msg guard counts: %v", err)
		return nil, err
	}
	if verdict := agentMessageGuardVerdict(counts); verdict != "" {
		result.Detail = verdict
		return result, nil
	}
	message := inbox.Message{ID: uuid.NewString(), Sender: senderParty, Body: content, CreatedAt: now.UTC().Format(time.RFC3339Nano), AsUser: a.AsUser}
	switch {
	case a.Steer:
		message.Turn = protocol.PeerTurnSteer
	case a.Interrupt:
		message.Turn = protocol.PeerTurnInterrupt
	}
	if err := d.store.PutPeerMessage(message, address); err != nil {
		return nil, err
	}
	receipt := d.deliverSavedInbox(address, message.ID, now)
	result.MessageID = receipt.ItemID
	result.Status = protocol.AgentMsgStatusQueued
	if receipt.Rang {
		result.Status = protocol.AgentMsgStatusNotified
	}
	result.Detail = receipt.Detail
	holder := d.inboxHolder(address)
	if holder != nil {
		result.TargetSessionID = holder.ID
	}
	if key, member := address.Member(); receipt.Rang && member {
		result.Detail = "notified " + d.crewMemberName(key.String())
	}
	if a.AsUser {
		result.AsUser = protocol.Ptr(true)
	}
	if receipt.Turn != "" && slices.Contains(receipt.Typed, message.ID) && holder != nil {
		result.Status, result.Detail = tookMidTurn(receipt.Turn, sessionDisplayName(holder), a.AsUser)
	} else if message.Turn != "" && !receipt.Rang && !slices.Contains(receipt.Typed, message.ID) && d.peerTurnLapsed(message.ID) {
		result.Detail = fmt.Sprintf("queued (the recipient could not take the %s mid-turn%s", message.Turn, turnLapsedSuffix)
	}
	return result, nil
}

func tookMidTurn(turn protocol.PeerTurn, recipient string, asUser bool) (protocol.AgentMsgStatus, string) {
	if turn == protocol.PeerTurnSteer {
		return protocol.AgentMsgStatusSteered, recipient + " gets your message at its next step; its turn goes on"
	}
	if asUser {
		return protocol.AgentMsgStatusInterrupted, recipient + "'s turn stopped; your text starts its next turn as the user's words"
	}
	return protocol.AgentMsgStatusInterrupted, recipient + "'s turn stopped; your message starts its next turn"
}

type agentMsgTargets struct {
	TargetSessionID string
	TargetSeedID    *string
}

func agentMsgTarget(target string) agentMsgTargets {
	target = strings.TrimSpace(target)
	if garden.ValidateID(target) == nil {
		return agentMsgTargets{TargetSeedID: protocol.Ptr(target)}
	}
	return agentMsgTargets{TargetSessionID: target}
}

func senderRefusal(sender, role string) *command.Refusal {
	return command.Refuse(role+"_session_not_found", "the %s %q is not a session on this daemon", role, sender)
}

func agentMessageQueuedDetail(err error) string {
	if errors.Is(err, errInboxNoPromptReader) {
		return "queued (target is a shell pane, so attn will not type at it; the message waits for `attn agent inbox` there)"
	}
	if errors.Is(err, errInboxDoorbellOutstanding) {
		return "queued (target already has an inbox doorbell; this message is in the same unread batch)"
	}
	if errors.Is(err, errInboxDoorbellInFlight) {
		return "queued (target's inbox doorbell is already being placed)"
	}
	if errors.Is(err, sessionmonitor.ErrBlockedByApproval) {
		return "queued (target is waiting on an approval — lands when the approval clears)"
	}
	if errors.Is(err, sessionmonitor.ErrUserTyping) {
		return "queued (the user typed in the target moments ago; lands once they have stopped typing for a while)"
	}
	if errors.Is(err, sessionmonitor.ErrSessionStopped) {
		return "queued (the user stopped the target; lands after the user's next prompt there)"
	}
	if errors.Is(err, sessionmonitor.ErrSessionClosing) {
		return "queued (target is compacting before it closes; lands once it has closed, under its standby rules)"
	}
	return queuedNotTakingInput
}

const turnLapsedSuffix = "; the message waits for the turn to end)"

func (d *Daemon) peerTurnLapsed(id string) bool {
	record, err := d.store.PeerMessageRecord(id)
	return err == nil && record.Message.Turn == ""
}

const queuedNotTakingInput = "queued (target is not taking input right now — lands when it is running again; don't wait for a reply)"

func sessionDisplayName(session *protocol.Session) string {
	return displayName(session.ID, session.Label, protocol.Deref(session.Title))
}

func displayName(id protocol.SessionID, label, title string) string {
	if label := strings.TrimSpace(label); label != "" {
		return label
	}
	if title != "" {
		return title
	}
	return shortSessionID(id)
}

func shortSessionID(id protocol.SessionID) string {
	if len(id) <= agentShortIDLength {
		return string(id)
	}
	return string(id[:agentShortIDLength])
}

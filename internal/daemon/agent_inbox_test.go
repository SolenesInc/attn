package daemon

import (
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/protocol"
)

func callAgentInbox(t *testing.T, d *Daemon, messageID, recipient string) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentInbox(conn, &protocol.AgentInboxMessage{
			Cmd: protocol.CmdAgentInbox, MessageID: protocol.Ptr(messageID), RecipientSessionID: recipient,
		})
	})
}

func callAgentInboxBatch(t *testing.T, d *Daemon, recipient string, limit int) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentInbox(conn, &protocol.AgentInboxMessage{
			Cmd: protocol.CmdAgentInbox, RecipientSessionID: recipient, Limit: protocol.Ptr(limit),
		})
	})
}

func callAgentMsgStatus(t *testing.T, d *Daemon, messageID, sender string) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentMsgStatus(conn, &protocol.AgentMsgStatusMessage{
			Cmd: protocol.CmdAgentMsgStatus, MessageID: messageID, SenderSessionID: sender,
		})
	})
}

func TestAgentMsgNotificationDoesNotWaitForInputTaken(t *testing.T) {
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	synctest.Test(t, func(t *testing.T) {
		previous := sessionInputTakenWindow
		sessionInputTakenWindow = time.Hour
		t.Cleanup(func() { sessionInputTakenWindow = previous })

		resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "the body stays in storage")
		result := resp.AgentMsgResult
		if result == nil || result.Status != protocol.AgentMsgStatusNotified {
			t.Fatalf("result = %+v, want notified", result)
		}
		prompts := doorbell.pasted()
		if len(prompts) != 1 || prompts[0] != agentMailboxDoorbellText {
			t.Fatalf("doorbell = %q", prompts)
		}
		if strings.Contains(prompts[0], "the body stays in storage") {
			t.Fatalf("doorbell leaked the body: %q", prompts[0])
		}
		record, err := d.store.PeerMessageRecord(result.MessageID)
		if err != nil || record.State() != agentmailbox.StateNotified {
			t.Fatalf("record = %+v, %v", record, err)
		}
	})
}

func TestAgentInboxReadsInFIFOOrderAndRearmsTheNextDoorbell(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d, doorbell := newAgentMsgDaemon(t)
		d.agentMailboxCooldownOverride = time.Second
		addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
		addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentCodex, protocol.SessionStateIdle)

		first := callAgentMsg(t, d, "target-session-id", "sender-session-id", "first body").AgentMsgResult
		time.Sleep(time.Nanosecond)
		second := callAgentMsg(t, d, "target-session-id", "sender-session-id", "second body").AgentMsgResult
		if first == nil || first.Status != protocol.AgentMsgStatusNotified || second == nil || second.Status != protocol.AgentMsgStatusQueued {
			t.Fatalf("send results = %+v / %+v", first, second)
		}
		if prompts := doorbell.pasted(); len(prompts) != 1 || prompts[0] != agentMailboxDoorbellText {
			t.Fatalf("doorbells before the first read = %q", prompts)
		}

		firstRead := callAgentInboxBatch(t, d, "target-session-id", 1)
		if !firstRead.Ok || firstRead.AgentInboxBatchResult == nil ||
			len(firstRead.AgentInboxBatchResult.Items) != 1 ||
			firstRead.AgentInboxBatchResult.Items[0].Content != "first body" ||
			firstRead.AgentInboxBatchResult.Remaining != 1 {
			t.Fatalf("first batch = %+v", firstRead.AgentInboxBatchResult)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if prompts := doorbell.pasted(); len(prompts) != 2 || prompts[1] != agentMailboxDoorbellText {
			t.Fatalf("doorbells after bounded read = %q", prompts)
		}

		secondRead := callAgentInboxBatch(t, d, "target-session-id", 1)
		if !secondRead.Ok || secondRead.AgentInboxBatchResult == nil ||
			len(secondRead.AgentInboxBatchResult.Items) != 1 ||
			secondRead.AgentInboxBatchResult.Items[0].Content != "second body" ||
			secondRead.AgentInboxBatchResult.Remaining != 0 {
			t.Fatalf("second batch = %+v", secondRead.AgentInboxBatchResult)
		}
	})
}

func TestAgentInboxReadWithNoCachedDoorbellStateKeepsRemainingUnread(t *testing.T) {
	d, _ := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "mailbox-target", protocol.SessionAgentCodex, protocol.SessionStateWorking)
	base := time.Date(2026, time.September, 3, 13, 0, 0, 0, time.UTC)
	if _, err := d.store.EnqueueMaintenancePrompt("uncached-first", "mailbox-target", "first", base); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.EnqueueMaintenancePrompt("uncached-second", "mailbox-target", "second", base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	response := callAgentInboxBatch(t, d, "mailbox-target", 1)
	result := response.AgentInboxBatchResult
	if !response.Ok || result == nil || len(result.Items) != 1 ||
		result.Items[0].ItemID != "uncached-first" || result.Remaining != 1 {
		t.Fatalf("batch = %+v, want first item and one remaining", result)
	}
	if !d.hasQueuedAgentMailboxItems("mailbox-target") {
		t.Fatal("remaining unread item was not restored into the doorbell cache")
	}
}

func TestAgentInboxRefusesUnauthorizedAndQueuedReads(t *testing.T) {
	d, _ := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStatePendingApproval)
	addCharacterizationSession(t, d, "other-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	sent := callAgentMsg(t, d, "target-session-id", "sender-session-id", "wait for the notification").AgentMsgResult
	if sent == nil || sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("send = %+v", sent)
	}
	for _, recipient := range []string{"other-session-id", "target-session-id"} {
		resp := callAgentInbox(t, d, sent.MessageID, recipient)
		if resp.Ok {
			t.Fatalf("%s read queued message: %+v", recipient, resp)
		}
	}
	record, err := d.store.PeerMessageRecord(sent.MessageID)
	if err != nil || record.State() != agentmailbox.StateQueued {
		t.Fatalf("failed reads changed record = %+v, %v", record, err)
	}
	status := callAgentMsgStatus(t, d, sent.MessageID, "other-session-id")
	if status.Ok || protocol.Deref(status.ErrorCode) != "message_not_found" {
		t.Fatalf("unauthorized status = %+v", status)
	}
}

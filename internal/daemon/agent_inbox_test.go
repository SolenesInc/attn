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
			Cmd: protocol.CmdAgentInbox, MessageID: messageID, RecipientSessionID: recipient,
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
		if len(prompts) != 1 || !strings.Contains(prompts[0], "attn agent inbox "+result.MessageID) {
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
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentCodex, protocol.SessionStateIdle)

	first := callAgentMsg(t, d, "target-session-id", "sender-session-id", "first body").AgentMsgResult
	second := callAgentMsg(t, d, "target-session-id", "sender-session-id", "second body").AgentMsgResult
	if first == nil || first.Status != protocol.AgentMsgStatusNotified || second == nil || second.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("send results = %+v / %+v", first, second)
	}
	if prompts := doorbell.pasted(); len(prompts) != 1 {
		t.Fatalf("typed %d doorbells before the first read: %q", len(prompts), prompts)
	}

	drained := make(chan int, 1)
	d.agentMailboxDrainHook = func(_ string, delivered int) { drained <- delivered }
	read := callAgentInbox(t, d, first.MessageID, "target-session-id")
	if !read.Ok || read.AgentInboxResult == nil || read.AgentInboxResult.Content != "first body" || read.AgentInboxResult.State != protocol.AgentMessageStateRead {
		t.Fatalf("first read = %+v", read)
	}
	select {
	case delivered := <-drained:
		if delivered != 1 {
			t.Fatalf("drained %d messages, want 1", delivered)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second notification was not rearmed")
	}
	prompts := doorbell.pasted()
	if len(prompts) != 2 || !strings.Contains(prompts[1], second.MessageID) || strings.Contains(prompts[1], "second body") {
		t.Fatalf("doorbells after read = %q", prompts)
	}
	status := callAgentMsgStatus(t, d, second.MessageID, "sender-session-id")
	if !status.Ok || status.AgentMsgStatusResult == nil || status.AgentMsgStatusResult.State != protocol.AgentMessageStateNotified {
		t.Fatalf("second status = %+v", status)
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

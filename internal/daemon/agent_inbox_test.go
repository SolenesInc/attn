package daemon

import (
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func callAgentInboxBatch(t *testing.T, d *Daemon, recipient string, limit int) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentInbox(conn, &protocol.AgentInboxMessage{
			Cmd: protocol.CmdAgentInbox, RecipientSessionID: recipient, Limit: protocol.Ptr(limit),
		})
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
		time.Sleep(time.Second)
		synctest.Wait()
		if prompts := doorbell.pasted(); len(prompts) != 2 {
			t.Fatalf("doorbells after the final read = %q, want no further reminder", prompts)
		}
	})
}

package daemon_test

import (
	"fmt"
	"github.com/victorarias/attn/internal/testworld"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

const inboxDoorbell = "You have unread items in your attn inbox. Run attn agent inbox to read them."

func TestAgentInboxReadsItsOwnMessagesOldestFirstInBoundedBatches(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	registerSessions(t, w, cli, "sender", "target", "elsewhere")

	for _, body := range []string{"first", "second", "third"} {
		sendAgentMessage(t, cli, "sender", "target", body)
	}
	sendAgentMessage(t, cli, "sender", "elsewhere", "not yours")

	batch := readInbox(t, cli, "target", 2)
	if got := inboxContents(batch.Items); got != "first second" || batch.Remaining != 1 {
		t.Fatalf("first batch = %q with %d remaining, want the two oldest and one left", got, batch.Remaining)
	}
	if sender := protocol.Deref(batch.Items[0].SenderSessionID); sender != "sender" {
		t.Errorf("the message names sender %q", sender)
	}
	if last := readInbox(t, cli, "target", 2); inboxContents(last.Items) != "third" || last.Remaining != 0 {
		t.Fatalf("second batch = %q with %d remaining", inboxContents(last.Items), last.Remaining)
	}
	if empty := readInbox(t, cli, "target", 2); len(empty.Items) != 0 {
		t.Errorf("a read inbox returned %q again", inboxContents(empty.Items))
	}
	if other := readInbox(t, cli, "elsewhere", 0); inboxContents(other.Items) != "not yours" {
		t.Errorf("the other recipient read %q", inboxContents(other.Items))
	}
}

func TestAgentInboxDefaultsToTwentyAndRefusesMailPastTheQueueCap(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	senders := []string{"s0", "s1", "s2", "s3", "s4", "s5", "s6"}
	registerSessions(t, w, cli, append(senders, "target")...)

	for i := range 50 {
		sendAgentMessage(t, cli, senders[i/8], "target", fmt.Sprintf("message %02d", i))
	}
	refused, err := cli.AgentMsg("target", "s6", "one too many")
	if err != nil {
		t.Fatal(err)
	}
	if refused.Status != protocol.AgentMsgStatusRefused || !strings.Contains(refused.Detail, "50") {
		t.Errorf("the 51st unread message = %+v, want a refusal naming the queue cap", refused)
	}

	first := readInbox(t, cli, "target", 0)
	if len(first.Items) != 20 || first.Remaining != 30 || first.Items[0].Content != "message 00" {
		t.Fatalf("default batch = %d items from %q, %d remaining; want the oldest 20 and 30 left",
			len(first.Items), first.Items[0].Content, first.Remaining)
	}
	rest := readInbox(t, cli, "target", 30)
	if len(rest.Items) != 30 || rest.Remaining != 0 || rest.Items[29].Content != "message 49" {
		t.Fatalf("the rest = %d items, %d remaining; want the other 30", len(rest.Items), rest.Remaining)
	}
}

func TestAgentMessageRefusesARecentDuplicateFromTheSameSender(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "sender", "someone-else", "target")

		sendAgentMessage(t, cli, "sender", "target", "same words")
		duplicate, err := cli.AgentMsg("target", "sender", "same words")
		if err != nil {
			t.Fatal(err)
		}
		if duplicate.Status != protocol.AgentMsgStatusRefused || !strings.Contains(duplicate.Detail, "already sent that exact text") {
			t.Fatalf("a duplicate within the window = %+v, want it refused with the reason", duplicate)
		}
		sendAgentMessage(t, cli, "someone-else", "target", "same words")

		w.advance(11 * time.Second)
		sendAgentMessage(t, cli, "sender", "target", "same words")
		if got := inboxContents(readInbox(t, cli, "target", 0).Items); got != "same words same words same words" {
			t.Errorf("inbox = %q, want the first send, the other sender's, and the stale repeat", got)
		}
	})
}

func TestAgentMessageIsReadableByIDOnlyByItsRecipientAndSurvivesARestart(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	recipient := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(recipient)
	app.TypeLine(recipient, "wait for the reviewer")
	agent.Prompted()
	agent.Reply("Waiting. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	registerSessions(t, w, cli, "reviewer", "bystander")

	sent := sendAgentMessage(t, cli, "reviewer", recipient, "the discount is applied after tax")
	if sent.Status != protocol.AgentMsgStatusNotified {
		t.Fatalf("message to an idle agent = %+v, want notified", sent)
	}
	if got := agent.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the recipient was prompted with %q, want the inbox doorbell", got)
	}
	if _, err := cli.AgentInbox(sent.MessageID, "bystander"); err == nil {
		t.Error("another session read the message by its ID")
	}

	w.restart()
	app = w.App()
	w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ID = recipient
		m.ResumeSessionID = protocol.Ptr(recipient)
	})
	if got := w.Launched(recipient).Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("after the restart the recipient was prompted with %q, want the doorbell again", got)
	}

	read, err := cli.AgentInbox(sent.MessageID, recipient)
	if err != nil {
		t.Fatalf("the recipient reads its message by ID: %v", err)
	}
	if read.Content != "the discount is applied after tax" || read.State != protocol.AgentMessageStateRead || read.ReadAt == nil {
		t.Fatalf("read = %+v", read)
	}
	again, err := cli.AgentInbox(sent.MessageID, recipient)
	if err != nil || protocol.Deref(again.ReadAt) != protocol.Deref(read.ReadAt) {
		t.Errorf("a repeat read = %+v, %v; want the same message read at the same time", again, err)
	}
	if left := readInbox(t, cli, recipient, 0); len(left.Items) != 0 {
		t.Errorf("the inbox still holds %q after the message was read", inboxContents(left.Items))
	}
}

func registerSessions(t *testing.T, w *world, cli *client.Client, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := cli.Register(id, id, w.Path(id)); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}
}

func sendAgentMessage(t *testing.T, cli *client.Client, from, to, body string) *protocol.AgentMsgResult {
	t.Helper()
	result, err := cli.AgentMsg(to, from, body)
	if err != nil {
		t.Fatalf("%s messages %s: %v", from, to, err)
	}
	if result.Status == protocol.AgentMsgStatusRefused {
		t.Fatalf("%s messaging %s %q was refused: %s", from, to, body, result.Detail)
	}
	return result
}

func readInbox(t *testing.T, cli *client.Client, recipient string, limit int) *protocol.AgentInboxBatchResult {
	t.Helper()
	batch, err := cli.AgentInboxBatch(recipient, limit)
	if err != nil {
		t.Fatalf("%s reads its inbox: %v", recipient, err)
	}
	return batch
}

func inboxContents(items []protocol.AgentInboxItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.Content)
	}
	return strings.Join(parts, " ")
}

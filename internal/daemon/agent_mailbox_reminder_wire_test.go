package daemon_test

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestUnreadMailRingsAgainEachQuietWindowUntilTheInboxIsRead(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")

		if sent := sendAgentMessage(t, cli, "sender", recipient.id, "the migration landed"); sent.Status != protocol.AgentMsgStatusNotified {
			t.Fatalf("mail for an idle agent = %+v, want notified", sent)
		}
		recipient.reply("Noted. <!-- attn:state=idle -->")
		if got := recipient.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("the doorbell rang %d times on arrival, want once: %q", got, recipient.prompts())
		}

		w.advance(29 * time.Second)
		if got := recipient.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("the doorbell rang again inside the quiet window: %q", recipient.prompts())
		}
		w.advance(time.Second)
		if got := recipient.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("unread mail did not ring again after the quiet window: %q", recipient.prompts())
		}

		if got := inboxContents(readInbox(t, cli, recipient.id, 0).Items); got != "the migration landed" {
			t.Fatalf("the agent read %q", got)
		}
		recipient.reply("Read it. <!-- attn:state=idle -->")
		w.advance(time.Minute)
		if got := recipient.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("the doorbell kept ringing after the inbox was read: %q", recipient.prompts())
		}
	})
}

func TestMailForAnAgentUnderAFreshDraftRingsOnceTheUserIsQuiet(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")

		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: recipient.id, Data: "half a thought"})
		w.advance(10 * time.Second)
		held := sendAgentMessage(t, cli, "sender", recipient.id, "the build is green")
		if held.Status != protocol.AgentMsgStatusQueued || !strings.Contains(held.Detail, "typed") {
			t.Fatalf("mail beside a fresh draft = %+v, want it queued behind the user's typing", held)
		}
		w.advance(19 * time.Second)
		if pasted := recipient.term.Pasted(); len(pasted) != 0 {
			t.Fatalf("attn pasted %q into a composer the user typed in %s ago", pasted, 29*time.Second)
		}
		w.advance(time.Second)
		if pasted := recipient.term.Pasted(); len(pasted) != 1 || !strings.Contains(pasted[0], inboxDoorbell) {
			t.Fatalf("once the user was quiet for the window attn pasted %q, want the inbox doorbell", pasted)
		}
	})
}

func TestAReminderHeldByAFreshDraftRingsOnceTheUserIsQuiet(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")

		sendAgentMessage(t, cli, "sender", recipient.id, "the build is green")
		recipient.reply("Later. <!-- attn:state=idle -->")
		w.advance(15 * time.Second)
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: recipient.id, Data: "half a thought"})

		w.advance(29 * time.Second)
		if got := recipient.promptsContaining(inboxDoorbell); got != 1 || len(recipient.term.Pasted()) != 1 {
			t.Fatalf("the reminder landed on a draft typed %s ago: pasted %q", 29*time.Second, recipient.term.Pasted())
		}
		w.advance(time.Second)
		if pasted := recipient.term.Pasted(); len(pasted) != 2 || !strings.Contains(pasted[1], inboxDoorbell) {
			t.Fatalf("once the user was quiet the reminder did not ring: pasted %q", pasted)
		}
	})
}

func TestMailArrivingAfterAReminderWaitsForTheNextRing(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")

		sendAgentMessage(t, cli, "sender", recipient.id, "first")
		recipient.reply("Later. <!-- attn:state=idle -->")
		w.advance(30 * time.Second)
		recipient.reply("Still later. <!-- attn:state=idle -->")
		if got := recipient.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("want the arrival ring and one reminder, got %q", recipient.prompts())
		}

		for _, body := range []string{"second", "third", "fourth"} {
			w.advance(time.Millisecond)
			sendAgentMessage(t, cli, "sender", recipient.id, body)
		}
		synctest.Wait()
		if got := recipient.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("new mail rang inside the reminder's quiet window: %q", recipient.prompts())
		}
		w.advance(30 * time.Second)
		if got := recipient.promptsContaining(inboxDoorbell); got != 3 {
			t.Fatalf("after the window the burst rang %d times in all, want one more: %q", got, recipient.prompts())
		}
		if got := inboxContents(readInbox(t, cli, recipient.id, 0).Items); got != "first second third fourth" {
			t.Fatalf("the inbox holds %q", got)
		}
	})
}

func TestMailArrivingAfterAPartialReadWaitsForTheNextRing(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")

		sendAgentMessage(t, cli, "sender", recipient.id, "first")
		w.advance(time.Millisecond)
		sendAgentMessage(t, cli, "sender", recipient.id, "second")
		if batch := readInbox(t, cli, recipient.id, 1); inboxContents(batch.Items) != "first" || batch.Remaining != 1 {
			t.Fatalf("a read of one item = %+v", batch)
		}
		recipient.reply("Read one. <!-- attn:state=idle -->")
		w.advance(time.Millisecond)
		sendAgentMessage(t, cli, "sender", recipient.id, "third")
		synctest.Wait()
		if got := recipient.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("mail after a partial read rang inside the quiet window: %q", recipient.prompts())
		}
		w.advance(30 * time.Second)
		if got := recipient.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("the unread rest rang %d times in all, want one reminder: %q", got, recipient.prompts())
		}
		if got := inboxContents(readInbox(t, cli, recipient.id, 0).Items); got != "second third" {
			t.Fatalf("the rest of the inbox is %q", got)
		}
	})
}

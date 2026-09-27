package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestATicketNudgeHeldByTheUsersTypingLandsOnceTheyAreQuiet(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		worker := w.bubbleClaude(t, app, "worker")
		registerSessions(t, w, cli, "author", "other")
		createTicket(t, cli, worker.id, "fix the build", "fix-build")
		focusAgent(t, w, app, "other")
		commentOnTicket(t, cli, "author", "fix-build", "take a look")

		w.advance(20 * time.Second)
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: worker.id, Data: "half a thought"})
		w.advance(10 * time.Second)
		if pasted := worker.term.Pasted(); len(pasted) != 0 {
			t.Fatalf("the countdown pasted %q into a composer the user typed in 10s ago", pasted)
		}
		w.advance(19 * time.Second)
		if pasted := worker.term.Pasted(); len(pasted) != 0 {
			t.Fatalf("the held nudge pasted %q before the user was quiet for the window", pasted)
		}
		w.advance(time.Second)
		if pasted := worker.term.Pasted(); len(pasted) != 1 || !strings.Contains(pasted[0], inboxDoorbell) {
			t.Fatalf("once the user was quiet the nudge pasted %q, want the inbox doorbell", pasted)
		}
	})
}

func TestOnlyTheUsersOwnKeystrokesHoldAttnsDoorbell(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source *string
		holds  bool
	}{
		{"an untagged keystroke", nil, true},
		{"an empty source", protocol.Ptr(""), true},
		{"an automation write", protocol.Ptr("automation"), false},
		{"a padded automation write", protocol.Ptr("  automation  "), false},
		{"an attach replay", protocol.Ptr("attach_replay"), false},
		{"a mouse report", protocol.Ptr("pointer"), false},
		{"a terminal query reply", protocol.Ptr("response"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				recipient := w.bubbleClaude(t, app, "shop")
				registerSessions(t, w, cli, "sender")

				app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: recipient.id, Data: "x", Source: tc.source})
				w.advance(0)
				sent := sendAgentMessage(t, cli, "sender", recipient.id, "the build is green")
				if held := sent.Status == protocol.AgentMsgStatusQueued; held != tc.holds {
					t.Fatalf("mail right after %s = %+v, want held %v", tc.name, sent, tc.holds)
				}
			})
		})
	}
}

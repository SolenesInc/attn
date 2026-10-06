package daemon_test

import (
	"os"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAHaltRecordedBeforeTheSessionStartedDoesNotEndItsTurn(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		cwd := w.Path("s1")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		transcript := fakeagent.WriteClaudeTranscript(t, cwd, "")
		transcript.Prompt("write an essay on checkout flows")
		transcript.Halt()
		w.advance(time.Hour)

		if err := w.InjectSession("s1", "checkout essay", cwd, protocol.SessionAgentClaude); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := cli.ObserveAgentConversation(protocol.TerminalID(w.Terminal("s1")), transcript.ConversationID, transcript.Path); err != nil {
			t.Fatalf("bind the conversation: %v", err)
		}
		transcript.Prompt("just the outline then")
		if err := cli.UpdateStateFromHookEvidence(protocol.TerminalID(w.Terminal("s1")), protocol.StateWorking, "", "user_prompt_submit", "just the outline then"); err != nil {
			t.Fatalf("report the prompt taken: %v", err)
		}
		working := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		w.advance(10 * time.Second)

		updates := sessionUpdatesOf(app, "s1")
		for _, s := range updates[indexOfUpdate(updates, working):] {
			if s.State != protocol.SessionStateWorking {
				t.Fatalf("the halt from before the session started ended its turn: %s", describeUpdates(updates))
			}
		}
		if got := queriedSession(t, cli, "s1"); got.State != protocol.SessionStateWorking {
			t.Fatalf("the session is %s (%s), want still working", got.State, protocol.Deref(got.StateReason))
		}
	})
}

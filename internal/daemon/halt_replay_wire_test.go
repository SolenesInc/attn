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
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		cwd := w.Path("s1")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		transcript := fakeagent.WriteClaudeTranscript(t, cwd, "")
		transcript.Prompt("write an essay on checkout flows")
		transcript.Halt()
		w.advance(time.Hour)

		w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) {
			m.ID, m.Label, m.ResumeSessionID = "s1", protocol.Ptr("checkout essay"), protocol.Ptr(transcript.ConversationID)
		})
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

func TestAHaltFromAnEarlierTurnReplayedByARestartedWatcherLeavesTheQuestionOpen(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		cwd := w.Path("s1")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		transcript := fakeagent.WriteClaudeTranscript(t, cwd, "")
		transcript.Prompt("a turn before the session")
		w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) {
			m.ID, m.Label, m.ResumeSessionID = "s1", protocol.Ptr("checkout essay"), protocol.Ptr(transcript.ConversationID)
		})
		terminal := protocol.TerminalID(w.Terminal("s1"))
		bind := func() {
			if err := cli.ObserveAgentConversation(terminal, transcript.ConversationID, transcript.Path); err != nil {
				t.Fatalf("bind the conversation: %v", err)
			}
		}
		bind()
		takePrompt := func(prompt string) {
			transcript.Prompt(prompt)
			if err := cli.UpdateStateFromHookEvidence(terminal, protocol.StateWorking, "", "user_prompt_submit", prompt); err != nil {
				t.Fatalf("report the prompt taken: %v", err)
			}
			testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		}

		w.advance(time.Second)
		takePrompt("write an essay on checkout flows")
		w.advance(time.Second)
		transcript.Halt()
		w.advance(10 * time.Second)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "turn_aborted"
		})

		w.advance(time.Second)
		takePrompt("just the outline then")
		if err := cli.UpdateState(terminal, protocol.StateWaitingInput); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

		w.restart()
		w.advance(10 * time.Second)
		if got := queriedSession(t, w.Client(), "s1"); got.State != protocol.SessionStateWaitingInput {
			t.Fatalf("the session is %s (%s), want the question still open", got.State, protocol.Deref(got.StateReason))
		}
	})
}

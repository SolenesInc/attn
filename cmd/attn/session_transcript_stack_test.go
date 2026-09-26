package main_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type transcriptEvent struct {
	Cursor    string `json:"cursor"`
	Kind      string `json:"kind"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

func readTranscript(t *testing.T, s *testworld.Stack, args ...string) (events []transcriptEvent, cursor string) {
	t.Helper()
	read := s.Attn(append([]string{"session", "transcript"}, append(args, "--json")...)...)
	if read.Code != 0 {
		t.Fatalf("session transcript %q exited %d: %s", args, read.Code, read.Stderr)
	}
	for _, line := range strings.Split(strings.TrimSpace(read.Stdout), "\n") {
		if line == "" {
			continue
		}
		var event transcriptEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("session transcript --json printed %q: %v", line, err)
		}
		events = append(events, event)
	}
	_, cursor, _ = strings.Cut(strings.TrimSpace(read.Stderr), "cursor: ")
	return events, cursor
}

func transcriptTexts(events []transcriptEvent) []string {
	var texts []string
	for _, event := range events {
		texts = append(texts, event.Kind+": "+event.Text)
	}
	return texts
}

func converse(app *testworld.Peer, agent *fakeagent.Run, id, prompt, reply string) {
	app.T.Helper()
	app.TypeLine(id, prompt)
	agent.Prompted()
	agent.Reply(reply + " <!-- attn:state=idle -->")
	testworld.AwaitSession(app, id, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
}

func TestSessionTranscriptAndInstructionsReadTheTargetConversation(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))

	for _, args := range [][]string{
		{"transcript"},
		{"transcript", "--follow"},
		{"transcript", "target", "extra"},
		{"instructions"},
		{"instructions", "--question", "was it authorized?"},
		{"instructions", "target"},
		{"instructions", "target", "--question", " "},
		{"instructions", "target", "extra", "--question", "was it authorized?"},
	} {
		refused := s.Attn(append([]string{"session"}, args...)...)
		if refused.Code != 2 || !strings.HasPrefix(refused.Stderr, "session "+args[0]+": ") {
			t.Errorf("attn session %q exited %d with stderr %q, want a usage refusal", args, refused.Code, refused.Stderr)
		}
	}
	if absent := s.Attn("session", "transcript", "target"); absent.Code != 1 || !strings.Contains(absent.Stderr, "The target transcript is unavailable") {
		t.Errorf("transcript with no daemon exited %d: %q", absent.Code, absent.Stderr)
	}

	s.Start()
	app := s.App()
	claudeID := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	claude := s.Launched(claudeID)
	converse(app, claude, claudeID, "add a discount field", "Added the field.")

	events, cursor := readTranscript(t, s, claudeID)
	if got := transcriptTexts(events); len(got) != 2 || !strings.HasPrefix(got[0], "user: add a discount field") || !strings.HasPrefix(got[1], "assistant: Added the field.") || cursor == "" {
		t.Fatalf("transcript --json = %q with cursor %q, want the prompt and the reply", got, cursor)
	}
	text := s.Attn("session", "transcript", claudeID).Stdout
	requireLines(t, "transcript", text, events[0].Timestamp+"  USER", "\n  add a discount field\n", events[1].Timestamp+"  ASSISTANT", "\n  Added the field.")

	converse(app, claude, claudeID, "and apply it after tax", "Applied after tax.")
	resumed, next := readTranscript(t, s, claudeID, "--after", cursor)
	if got := transcriptTexts(resumed); len(got) != 2 || !strings.HasPrefix(got[0], "user: and apply it after tax") || next == cursor {
		t.Fatalf("transcript --after %s = %q with cursor %q, want only the second turn", cursor, got, next)
	}

	codexID := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	converse(app, s.Launched(codexID), codexID, "who approved the refund?", "Nobody yet.")
	if mismatch := s.Attn("session", "transcript", codexID, "--after", cursor); mismatch.Code != 1 || !strings.Contains(mismatch.Stderr, "belongs to a different transcript") {
		t.Errorf("another transcript's cursor exited %d: %q", mismatch.Code, mismatch.Stderr)
	}
	if unknown := s.Attn("session", "transcript", "no-such-session"); unknown.Code != 1 || !strings.Contains(unknown.Stderr, "The target session was not found") {
		t.Errorf("transcript of an unknown session exited %d: %q", unknown.Code, unknown.Stderr)
	}

	for _, tc := range []struct{ target, code, message string }{
		{"no-such-session", "session_not_found", "The target session was not found"},
		{claudeID, "transcript_unavailable", "The target transcript is unavailable"},
		{codexID, "model_unavailable", "The session-instructions model is unavailable"},
	} {
		asked := s.Attn("session", "instructions", tc.target, "--question", "Was the refund authorized?")
		if asked.Code != 1 || strings.TrimSpace(asked.Stderr) != tc.message {
			t.Errorf("instructions for %s exited %d with stderr %q, want %q", tc.target, asked.Code, asked.Stderr, tc.message)
		}
		var failure struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		asJSON := s.Attn("session", "instructions", tc.target, "--question", "Was the refund authorized?", "--json")
		asJSON.JSON(t, &failure)
		if asJSON.Code != 1 || failure.Error.Code != tc.code || failure.Error.Message != tc.message {
			t.Errorf("instructions --json for %s exited %d with %+v, want code %s", tc.target, asJSON.Code, failure.Error, tc.code)
		}
	}
}

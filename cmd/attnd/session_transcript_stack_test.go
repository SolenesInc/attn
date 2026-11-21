package main_test

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
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
		{"instructions", "target", "extra", "--question", "was it authorized?"},
	} {
		refused := s.Attn(append([]string{"session"}, args...)...)
		if refused.Code != 2 || !strings.HasPrefix(refused.Stderr, "session "+args[0]+": ") {
			t.Errorf("attn session %q exited %d with stderr %q, want a usage refusal", args, refused.Code, refused.Stderr)
		}
	}
	if absent := s.Attn("session", "transcript", "target"); absent.Code != 1 || !strings.Contains(absent.Stderr, "connect to daemon") {
		t.Errorf("transcript with no daemon exited %d: %q", absent.Code, absent.Stderr)
	}

	s.Start()
	for _, args := range [][]string{{"instructions", "target"}, {"instructions", "target", "--question", " "}} {
		refused := s.Attn(append([]string{"session"}, args...)...)
		if refused.Code != 2 || !strings.Contains(refused.Stderr, "--question is required") {
			t.Errorf("attn session %q exited %d with stderr %q, want a usage refusal", args, refused.Code, refused.Stderr)
		}
	}
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

func TestSessionInstructionsPrintsTheAnswerWithItsExcerptsAndTranscript(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Vars = append(s.Vars, "ATTN_HEADLESS_TASKS=on")
	const question = "Was the refund authorized?"
	quoted := regexp.MustCompile(`\[(\S+) user\] who approved the refund\?`)
	s.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		turn := quoted.FindStringSubmatch(task.Prompt)
		if !strings.Contains(task.Prompt, question) || turn == nil {
			task.Fail("not the question under test")
			return
		}
		task.Answer(`{"answer":"no","evidence":[{"turn_id":"` + turn[1] + `","quote":"who approved the refund?"}]}`)
	})
	s.Start()
	app := s.App()
	id := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	converse(app, s.Launched(id), id, "who approved the refund?", "Nobody yet.")

	asked := s.Attn("session", "instructions", id, "--question", question)
	requireStdout(t, asked, "No.\n\nuser — ", "\n  \"who approved the refund?\"\n\nTranscript: /")
}

func TestSessionTranscriptFollowKeepsReadingAcrossADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	claude := s.Launched(id)
	converse(app, claude, id, "add a discount field", "Added the field.")

	follow := s.Launch(testworld.Invocation{Args: []string{"session", "transcript", id, "--follow"}})
	follow.AwaitStdout("Added the field.")
	s.Stop()
	if err := os.Remove(s.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	lost, err := net.Listen("unix", s.Socket)
	if err != nil {
		t.Fatal(err)
	}
	dropped, err := lost.Accept()
	if err != nil {
		t.Fatal(err)
	}
	dropped.Close()
	lost.Close()
	s.Start()
	app = s.App()
	converse(app, claude, id, "and apply it after tax", "Applied after tax.")
	follow.AwaitStdout("Applied after tax.")
	_, cursor := readTranscript(t, s, id)
	if stopped := follow.Interrupt(); stopped.Code != 0 || !strings.Contains(stopped.Stderr, "cursor: "+cursor) {
		t.Errorf("--follow interrupted after a restart exited %d with stderr %q, want the cursor %s of the last page it printed", stopped.Code, stopped.Stderr, cursor)
	}
}

func TestSessionTranscriptFollowOnAnOutpostKeepsReadingAcrossAHomeRestart(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude)), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	homeAway := outpost.PauseAt(pausepoint.HomeForwardFailed)
	pair.Start()
	app := pair.App
	id := home.Spawn(app, fakeagent.Claude, home.Path("shop"))
	claude := home.Launched(id)
	converse(app, claude, id, "add a discount field", "Added the field.")

	follow := outpost.Launch(testworld.Invocation{Args: []string{"session", "transcript", id, "--follow"}})
	follow.AwaitStdout("Added the field.")
	home.Stop()
	homeAway.Await()
	homeAway.Release()
	name, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	once := outpost.Attn("session", "transcript", id)
	if refused := strings.TrimSpace(once.Stderr); once.Code != 1 || (refused != name+" is unreachable; this needs it now" && refused != "lost "+name+" during this call; it may have run") {
		t.Errorf("session transcript without --follow on the outpost with the home away exited %d with stderr %q, want the refusal naming %s", once.Code, once.Stderr, name)
	}
	t.Logf("without --follow, the home away: %q", once.Stderr)

	home.Start()
	app = home.App()
	outpostStateOnConnect(app, "outpost", protocol.OutpostStateConnected)
	converse(app, claude, id, "and apply it after tax", "Applied after tax.")
	follow.AwaitStdout("Applied after tax.")
	_, cursor := readTranscript(t, home, id)
	stopped := follow.Interrupt()
	if stopped.Code != 0 || !strings.Contains(stopped.Stderr, "cursor: "+cursor) {
		t.Errorf("--follow on the outpost interrupted after the home restarted exited %d with stderr %q, want the cursor %s of the last page it printed", stopped.Code, stopped.Stderr, cursor)
	}
	for _, reply := range []string{"Added the field.", "Applied after tax."} {
		if printed := strings.Count(stopped.Stdout, reply); printed != 1 {
			t.Errorf("--follow on the outpost printed %q %d times across the home's restart, want once: %q", reply, printed, stopped.Stdout)
		}
	}
}

func TestSessionTranscriptFollowStopsWhenItsReaderGoesAwayAndNamesTheLastPageItDelivered(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	claude := s.Launched(id)
	converse(app, claude, id, "add a discount field", "Added the field.")
	_, delivered := readTranscript(t, s, id)

	follow := s.Launch(testworld.Invocation{Binary: "/bin/sh", Args: []string{"-c", `"$0" session transcript "$1" --follow | head -c 1 >/dev/null`, testworld.AttnBinary(t), id}})
	converse(app, claude, id, "and apply it after tax", "Applied after tax.")
	stopped := follow.Wait()
	if stopped.Code != 0 || !strings.Contains(stopped.Stderr, "cursor: "+delivered) {
		t.Errorf("--follow into a closed pipe exited %d with stderr %q, want it to stop and name the cursor %s of the page it delivered", stopped.Code, stopped.Stderr, delivered)
	}
}

package daemon_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const folderTrustChoice = "Yes, I trust this folder"

func spawnClaudeAtFolderTrust(t *testing.T, w *world, app *testworld.Peer, prompt string) string {
	t.Helper()
	w.AskFolderTrustAtNextBoot()
	id := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr(prompt)
	})
	app.AwaitScreen(id, folderTrustChoice)
	w.advance(10 * time.Second)
	testworld.AwaitSession(app, id, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.StateReason) == "folder_trust_question"
	})
	return id
}

func TestAClaudeStoppedAtTheFolderTrustQuestionWaitsForTheUserAndStartsWhenAnswered(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	id := spawnClaudeAtFolderTrust(t, w, app, "Check the code.")

	app.TypeLine(id, "\x1b[B")

	agent := w.Launched(id)
	if opening := agent.Prompted(); !strings.Contains(opening, "Check the code.") {
		t.Fatalf("once trusted the agent was prompted %q, want its opening prompt", opening)
	}
	testworld.AwaitSession(app, id, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && protocol.Deref(s.StateReason) != "folder_trust_question"
	})
}

func TestRefusingTheFolderTrustQuestionEndsTheSessionAsAnyExitDoes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	id := spawnClaudeAtFolderTrust(t, w, app, "Check the code.")

	app.TypeLine(id, "")

	testworld.AwaitSession(app, id, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "process_exited"
	})
}

func delegateThatNeverStarts(t *testing.T, w *world, screen string) (protocol.DelegateResult, []protocol.SeedNote, string) {
	t.Helper()
	cli := w.Client()
	registerSessions(t, w, cli, "delegator")
	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request := brief(cwd, "Say hello")
	request.Agent = protocol.Ptr("claude")
	request.Label = protocol.Ptr("hello")
	request.SourceSessionID = protocol.Ptr(protocol.SessionID("delegator"))

	answered := make(chan *protocol.DelegateResult, 1)
	go func() {
		result, err := cli.Delegate(request)
		if err != nil {
			t.Errorf("delegating: %v", err)
		}
		answered <- result
	}()
	w.advance(60 * time.Second)
	var delegate protocol.Session
	for _, session := range w.App().Initial.Sessions {
		if session.Label == "hello" {
			delegate = session
		}
	}
	w.terminal(string(delegate.ID)).PaintScreen(screen)
	w.advance(30 * time.Second)
	result := <-answered

	notes, err := cli.SeedNotes("", protocol.Deref(delegate.SeedID), client.SeedNotesQuery{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	w.advance(0)
	return *result, notes.Notes, inboxContents(readInbox(t, cli, "delegator", 0).Items)
}

func TestADelegationStoppedAtTheFolderTrustQuestionRingsItsSeedNamingTheQuestion(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		result, notes, inbox := delegateThatNeverStarts(t, w, "Quick safety check\r\n❯ No, exit\r\n  "+folderTrustChoice)
		if said := protocol.Deref(result.FirstTurnUnconfirmed); !strings.Contains(said, "folder-trust question") {
			t.Errorf("the delegate result says %q, want it to name the folder-trust question", said)
		}
		if len(notes) == 0 || !strings.Contains(notes[0].Body, "stopped at its folder-trust question") || !strings.Contains(notes[0].Body, folderTrustChoice) {
			t.Errorf("the seed's notes are %+v, want a note naming the question with the screen", notes)
		}
		if !strings.Contains(inbox, "note") {
			t.Errorf("the delegator's inbox holds %q, want the seed's note rung to it", inbox)
		}
	})
}

func TestADelegationWhoseFirstTurnNeverComesRingsItsSeedWithTheScreen(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		result, notes, inbox := delegateThatNeverStarts(t, w, "Please log in with your browser")
		if said := protocol.Deref(result.FirstTurnUnconfirmed); strings.Contains(said, "folder-trust") {
			t.Errorf("the delegate result says %q, but the screen shows no folder-trust question", said)
		}
		if len(notes) == 0 || !strings.Contains(notes[0].Body, "reported no first turn") || !strings.Contains(notes[0].Body, "Please log in") {
			t.Errorf("the seed's notes are %+v, want a note with the screen", notes)
		}
		if !strings.Contains(inbox, "note") {
			t.Errorf("the delegator's inbox holds %q, want the seed's note rung to it", inbox)
		}
	})
}

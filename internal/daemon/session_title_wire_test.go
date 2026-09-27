package daemon_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func newTitlingWorld(t *testing.T, agents ...fakeagent.Harness) *world {
	t.Helper()
	w := newWorld(t, agents...)
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	return w
}

func awaitLabel(app *testworld.Peer, session, label string) {
	app.T.Helper()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.Label == label })
}

func (w *world) sessionLabel(session string) string {
	w.T.Helper()
	listed, err := w.Client().List("")
	if err != nil {
		w.T.Fatal(err)
	}
	for _, s := range listed.Sessions {
		if s.ID == session {
			return s.Label
		}
	}
	w.T.Fatalf("no session %s", session)
	return ""
}

func awaitTitleTask(app *testworld.Peer, session string, match func(protocol.Task) bool) protocol.Task {
	app.T.Helper()
	for {
		requestID := uuid.NewString()
		listed := testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(requestID)},
			protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == requestID })
		for _, task := range listed.Tasks {
			if task.Kind == "session_title" && task.Subject == session && match(task) {
				return task
			}
		}
		testworld.Await[protocol.TasksChangedMessage](app, protocol.EventTasksChanged, nil)
	}
}

func answerTurnVerdict(t *testing.T, w *world, verdict string) {
	t.Helper()
	task := w.HeadlessTask()
	if !strings.Contains(task.Prompt, "Classify how this assistant message ends the agent's turn") {
		t.Fatalf("the daemon asked %q, want the turn's verdict", task.Prompt)
	}
	task.Answer(`{"verdict":"` + verdict + `"}`)
}

func TestEachAgentTitlesASessionFromThePromptItWasSpawnedWith(t *testing.T) {
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex, fakeagent.Copilot} {
		t.Run(string(h), func(t *testing.T) {
			w := newTitlingWorld(t, h)
			app := w.App()
			session := w.Spawn(app, h, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
				m.InitialPrompt = protocol.Ptr("investigate the retry queue")
			})
			agent := w.Launched(session)

			task := w.HeadlessTask()
			if task.Harness != h || !strings.Contains(task.Prompt, "investigate the retry queue") {
				t.Fatalf("the title task went to %s asking %q, want %s asked about the initial prompt", task.Harness, task.Prompt, h)
			}
			task.Answer("Retry queue investigation")
			awaitLabel(app, session, "Retry queue investigation")
			agent.Prompted()
		})
	}
}

func TestTheFirstPromptTheUserTypesTitlesTheSession(t *testing.T) {
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(h), func(t *testing.T) {
			w := newTitlingWorld(t, h)
			app := w.App()
			session := w.Spawn(app, h, w.Path("shop"))
			agent := w.Launched(session)

			app.TypeLine(session, "the login form rejects valid passwords")
			agent.Prompted()
			task := w.HeadlessTask()
			if task.Harness != h || !strings.Contains(task.Prompt, "the login form rejects valid passwords") {
				t.Fatalf("the title task went to %s asking %q, want %s asked about the user's prompt", task.Harness, task.Prompt, h)
			}
			task.Answer("Fix login flow")
			awaitLabel(app, session, "Fix login flow")
		})
	}
}

func TestATitleIsTheModelsFirstLineWithoutQuotesPrefixOrTrailingPunctuation(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)

	app.TypeLine(session, "the login form rejects valid passwords")
	agent.Prompted()
	w.HeadlessTask().Answer("\n\"Title:  Fix   login flow.\"\nThe user wants the password check fixed.")
	awaitLabel(app, session, "Fix login flow")
}

func TestANameTheUserChoseIsNeverReplacedByATitle(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("checkout fixes")
	})
	agent := w.Launched(session)

	app.TypeLine(session, "the login form rejects valid passwords")
	agent.Prompted()
	agent.Reply("Fixed the password check. <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	settled := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if settled.Label != "checkout fixes" {
		t.Errorf("the session is named %q, want the user's %q", settled.Label, "checkout fixes")
	}
}

func TestASessionNamedByItsIDIsTitled(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("s-7k3f9m")
	})
	agent := w.Launched(session)

	app.TypeLine(session, "the login form rejects valid passwords")
	agent.Prompted()
	w.HeadlessTask().Answer("Fix login flow")
	awaitLabel(app, session, "Fix login flow")
}

func TestRenamingWhileTheTitleIsGeneratedKeepsTheUsersName(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)

	app.TypeLine(session, "the login form rejects valid passwords")
	agent.Prompted()
	task := w.HeadlessTask()
	renameFromApp(app, protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: session, Label: "password bug"}, session)
	task.Answer("Fix login flow")
	awaitTitleTask(app, session, func(task protocol.Task) bool { return task.State == "done" })
	if label := w.sessionLabel(session); label != "password bug" {
		t.Errorf("the session is named %q after its title arrived, want the user's %q", label, "password bug")
	}
}

func TestAFailedTitleKeepsTheNameUntilTheUserRetries(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)

	app.TypeLine(session, "the login form rejects valid passwords")
	agent.Prompted()
	w.HeadlessTask().Fail("API Error: 529 overloaded")
	failed := awaitTitleTask(app, session, func(task protocol.Task) bool { return task.State == "failed" })
	if !strings.Contains(protocol.Deref(failed.LastDiagnostic), "529 overloaded") {
		t.Errorf("the failed title task reports %q, want the model's error", protocol.Deref(failed.LastDiagnostic))
	}
	if label := w.sessionLabel(session); label != "shop" {
		t.Errorf("after a failed title the session is named %q, want %q", label, "shop")
	}
	agent.Reply("Fixed the password check. <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	app.TypeLine(session, "now add a test for it")
	agent.Prompted()
	agent.Reply("Added the test. <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")

	requestID := uuid.NewString()
	testworld.Request(app, protocol.TaskRetryMessage{Cmd: protocol.CmdTaskRetry, TaskID: failed.ID, RequestID: protocol.Ptr(requestID)},
		protocol.EventTaskRetryResult, func(r protocol.TaskRetryResultMessage) bool { return r.RequestID == requestID })
	task := w.HeadlessTask()
	if !strings.Contains(task.Prompt, "the login form rejects valid passwords") {
		t.Fatalf("the retried title task prompt %q does not carry the user's prompt", task.Prompt)
	}
	task.Answer("Fix login flow")
	awaitLabel(app, session, "Fix login flow")
}

func TestACrewMembersDayKeepsTheMembersName(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	app, cli := w.App(), w.Client()
	woken := wakeCrew(t, cli, "trellis", "")
	agent := w.Launched(woken.SessionID)
	named := testworld.AwaitSession(app, woken.SessionID, func(s protocol.Session) bool { return protocol.Deref(s.CrewMember) == "trellis" })

	agent.Prompted()
	agent.Reply("I am Trellis. <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	app.TypeLine(woken.SessionID, "the login form rejects valid passwords")
	if got := agent.Prompted(); got != "the login form rejects valid passwords" {
		t.Fatalf("the member was prompted %q", got)
	}
	agent.Reply("Fixed the password check. <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	settled := testworld.AwaitSession(app, woken.SessionID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if settled.Label != named.Label {
		t.Errorf("the member's day is named %q, want %q", settled.Label, named.Label)
	}
}

package daemon_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
		if string(s.ID) == session {
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

func TestACodexInitialPromptTitlesItsSessionAfterItsConversationBinds(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude, fakeagent.Codex)
	app := w.App()
	first := w.Spawn(app, fakeagent.Claude, w.Path("first"))
	claude := w.Launched(first)
	app.TypeLine(first, "find the flaky test")
	claude.Prompted()
	firstTitle := w.HeadlessTask()

	boot := w.HoldNextBoot()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("investigate the retry queue")
	})
	awaitTitleTask(app, session, func(task protocol.Task) bool { return task.State == "queued" })
	boot.Await()
	boot.Release()
	codex := w.Launched(session)
	if prompt := codex.Prompted(); prompt != "investigate the retry queue" {
		t.Fatalf("Codex received %q, want the initial prompt", prompt)
	}
	read, err := w.Client().SessionTranscript(session, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Events) != 1 || read.Events[0].Kind != "user" || protocol.Deref(read.Events[0].Text) != "investigate the retry queue" {
		t.Fatalf("session transcript = %+v, want the bound conversation's initial prompt before the title runs", read.Events)
	}

	firstTitle.Answer("Flaky test hunt")
	awaitLabel(app, first, "Flaky test hunt")
	task := w.HeadlessTask()
	if task.Harness != fakeagent.Codex || !strings.Contains(task.Prompt, "investigate the retry queue") {
		t.Fatalf("the title task went to %s asking %q, want Codex asked about the initial prompt", task.Harness, task.Prompt)
	}
	task.Answer("Retry queue investigation")
	awaitLabel(app, session, "Retry queue investigation")
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

func TestASharedCodexTitleStaysWithItsConversationWhenTheTileMovesOn(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Codex)
	app := w.App()
	first := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(first)
	app.TypeLine(first, "find the flaky test")
	codex.Prompted()
	firstTitle := w.HeadlessTask()

	app.TypeLine(first, "/new")
	codex.Prompted()
	next := awaitSuccessor(app, first)
	app.TypeLine(string(next.ID), "the cart total ignores discounts")
	codex.Prompted()
	firstTitle.Answer("Flaky test hunt")
	awaitLabel(app, first, "Flaky test hunt")
	if label := w.sessionLabel(string(next.ID)); label == "Flaky test hunt" {
		t.Fatal("the first conversation's title replaced the new conversation's label")
	}
	task := w.HeadlessTask()
	if !strings.Contains(task.Prompt, "the cart total ignores discounts") {
		t.Fatalf("the new conversation's title task asked %q", task.Prompt)
	}
	task.Answer("Fix cart discounts")
	awaitLabel(app, string(next.ID), "Fix cart discounts")
}

func TestTheFirstPromptTypedIntoACodexNewChatTitlesItsNewSession(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Codex)
	app := w.App()
	first := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(first)
	app.TypeLine(first, "find the flaky test")
	codex.Prompted()
	w.HeadlessTask().Answer("Flaky test hunt")
	awaitLabel(app, first, "Flaky test hunt")

	app.TypeLine(first, "/new")
	if got := codex.Prompted(); got != "/new" {
		t.Fatalf("codex received %q, want /new", got)
	}
	next := awaitSuccessor(app, first)
	app.TypeLine(string(next.ID), "the cart total ignores discounts")
	codex.Prompted()
	task := w.HeadlessTask()
	if !strings.Contains(task.Prompt, "the cart total ignores discounts") {
		t.Fatalf("the title task asked %q, want the new chat's first prompt", task.Prompt)
	}
	task.Answer("Fix cart discounts")
	awaitLabel(app, string(next.ID), "Fix cart discounts")
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
	renameFromApp(app, protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: protocol.SessionID(session), Label: "password bug"}, session)
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

func TestAFailureNotificationClearsWhenTheRetrySucceeds(t *testing.T) {
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		var recovered atomic.Bool
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
			switch {
			case strings.HasPrefix(task.Prompt, "Classify how this assistant message ends"):
				task.Answer(`{"verdict":"DONE"}`)
			case recovered.Load():
				task.Answer("Fix login flow")
			default:
				task.Fail("API Error: 529 overloaded")
			}
		})
		app := w.App()
		session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
			m.InitialPrompt = protocol.Ptr("the login form rejects valid passwords")
		})
		w.advance(0)
		w.advance(time.Minute)
		w.advance(2 * time.Minute)
		if feed := listNotifications(app); len(feed.Notifications) != 1 || feed.UnreadCount != 1 {
			t.Fatalf("after the failure the feed = %+v, want one unread warning", feed)
		}

		recovered.Store(true)
		failed := awaitTitleTask(app, session, func(task protocol.Task) bool { return task.State == "dead" || task.State == "failed" })
		requestID := uuid.NewString()
		testworld.Request(app, protocol.TaskRetryMessage{Cmd: protocol.CmdTaskRetry, TaskID: failed.ID, RequestID: protocol.Ptr(requestID)},
			protocol.EventTaskRetryResult, func(r protocol.TaskRetryResultMessage) bool { return r.RequestID == requestID })
		w.advance(0)
		awaitLabel(app, session, "Fix login flow")
		if feed := listNotifications(app); len(feed.Notifications) != 1 || feed.UnreadCount != 0 || len(feed.Notifications[0].Actions) != 0 {
			t.Errorf("after the retry succeeded the feed = %+v, want the warning read with no actions", feed)
		}
	})
}

func TestACrewMembersDayKeepsTheMembersName(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	app, cli := w.App(), w.Client()
	woken := startCrew(t, cli, "trellis", "")
	agent := w.Launched(string(woken.SessionID))
	named := testworld.AwaitSession(app, string(woken.SessionID), func(s protocol.Session) bool { return protocol.Deref(s.CrewMember) == "trellis" })

	agent.Prompted()
	agent.Reply("I am Trellis. <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	app.TypeLine(string(woken.SessionID), "the login form rejects valid passwords")
	if got := agent.Prompted(); got != "the login form rejects valid passwords" {
		t.Fatalf("the member was prompted %q", got)
	}
	agent.Reply("Fixed the password check. <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	settled := testworld.AwaitSession(app, string(woken.SessionID), func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if settled.Label != named.Label {
		t.Errorf("the member's day is named %q, want %q", settled.Label, named.Label)
	}
}

func TestAFailedTitleWarnsOnlyTheProfileThatOwnsTheSession(t *testing.T) {
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
			if strings.HasPrefix(task.Prompt, "Classify how this assistant message ends") {
				task.Answer(`{"verdict":"DONE"}`)
				return
			}
			task.Fail("API Error: 529 overloaded")
		})
		defaultApp := w.App()
		side := createProfile(defaultApp, "Side")
		sideApp := w.AppOn(side.ID)
		w.Spawn(sideApp, fakeagent.Claude, w.Path("side-shop"), func(m *protocol.SpawnSessionMessage) {
			m.InitialPrompt = protocol.Ptr("the login form rejects valid passwords")
		})
		w.advance(0)
		w.advance(time.Minute)
		w.advance(2 * time.Minute)

		sideFeed := listNotifications(sideApp)
		if len(sideFeed.Notifications) != 1 || sideFeed.UnreadCount != 1 || sideFeed.Notifications[0].Kind != "task_failed" ||
			!strings.Contains(sideFeed.Notifications[0].Title, "side-shop") {
			t.Fatalf("Side's feed = %+v, want one unread warning naming its session", sideFeed)
		}
		if defaultFeed := listNotifications(defaultApp); len(defaultFeed.Notifications) != 0 || defaultFeed.UnreadCount != 0 {
			t.Fatalf("a failure for Side's session reached Default: %+v", defaultFeed)
		}
	})
}

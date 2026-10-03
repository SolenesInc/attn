package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheActivityLineSaysWhatTheAgentDidSinceTheLastLineWhileTheUserWatches(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "activity.enabled", "true")
	setSetting(t, app, "activity.config", `{"agent":"claude"}`)
	session, agent := spawnActivitySession(w, app)

	activityTurn(t, w, app, agent, session, "read the plan", "I read the plan.")
	if status := activityStatus(t, cli); status.PresenceTier != "away" {
		t.Fatalf("with nobody reporting presence the tier is %q, want away", status.PresenceTier)
	}
	if task, ok := activityTask(app, session); ok {
		t.Fatalf("a turn ended while nobody watched queued activity task %+v", task)
	}

	app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, DashboardVisible: true})
	activityTurn(t, w, app, agent, session, "start on the tests", "Starting on the tests.")
	awaitActivityTask(app, session, func(task protocol.Task) bool { return task.State == "done" })

	activityTurn(t, w, app, agent, session, "run the frontend tests", "The frontend suite is running.")
	task := w.HeadlessTask()
	if task.Harness != fakeagent.Claude || task.Model != "claude-haiku-4-5" || task.Effort != "" {
		t.Errorf("the activity task ran %s %q at effort %q, want Claude's default claude-haiku-4-5", task.Harness, task.Model, task.Effort)
	}
	if !strings.Contains(task.Prompt, "The frontend suite is running.") || strings.Contains(task.Prompt, "Starting on the tests.") || strings.Contains(task.Prompt, "I read the plan.") {
		t.Errorf("the activity prompt %q, want only the output since the line was last seeded", task.Prompt)
	}
	task.Answer("Running the frontend test suite.")
	awaitActivity(app, session, "Running the frontend test suite")
	status := activityStatus(t, cli)
	if status.PresenceTier != "watching" || !status.Enabled || status.Error != nil || len(status.Sessions) != 1 ||
		protocol.Deref(status.Sessions[0].Activity) != "Running the frontend test suite" || status.Sessions[0].ActivityAt == nil {
		t.Errorf("activity status = %+v, want the watching tier and the session's dated line", status)
	}

	setSetting(t, app, "activity.config", `{"agent":"codex"}`)
	activityTurn(t, w, app, agent, session, "fix the cart test", "The cart test passes now.")
	task = w.HeadlessTask()
	if task.Harness != fakeagent.Codex || task.Model != "gpt-5.6-luna" || task.Effort != "low" {
		t.Errorf("the activity task ran %s %q at effort %q, want Codex's default gpt-5.6-luna at low", task.Harness, task.Model, task.Effort)
	}
	if !strings.Contains(task.Prompt, "Running the frontend test suite") {
		t.Errorf("the activity prompt %q does not carry the previous line", task.Prompt)
	}
	task.Answer("Fixing the failing cart test")
	awaitActivity(app, session, "Fixing the failing cart test")

	activityTurn(t, w, app, agent, session, "run the whole suite", "The whole suite is running.")
	w.HeadlessTask().Fail("not logged in")
	awaitActivityTask(app, session, func(task protocol.Task) bool { return task.State == "failed" })
	if failed := activitySession(t, cli, session); !strings.Contains(protocol.Deref(failed.Error), "authentication failed") || protocol.Deref(failed.Activity) != "Fixing the failing cart test" {
		t.Errorf("activity status after a failed run = error %q beside line %q, want the failure beside the last good line", protocol.Deref(failed.Error), protocol.Deref(failed.Activity))
	}

	setSetting(t, app, "activity.config", `{"agent":"codex","model":"gpt-5.6","effort":"medium"}`)
	activityTurn(t, w, app, agent, session, "update the changelog", "Updated the changelog.")
	task = w.HeadlessTask()
	if task.Model != "gpt-5.6" || task.Effort != "medium" {
		t.Errorf("the activity task ran %q at effort %q, want the chosen gpt-5.6 at medium", task.Model, task.Effort)
	}
	task.Answer("  \n  ")
	awaitActivityTask(app, session, func(task protocol.Task) bool { return task.State == "done" })
	if line := protocol.Deref(activitySession(t, cli, session).Activity); line != "Fixing the failing cart test" {
		t.Errorf("after an empty answer the line is %q, want the previous one kept", line)
	}

	if err := cli.ClearSessionActivity(session); err != nil {
		t.Fatal(err)
	}
	if line := protocol.Deref(activitySession(t, cli, session).Activity); line != "" {
		t.Errorf("after clearing it the line is %q", line)
	}
}

func TestTurningActivityOffClearsEveryLineAndKeepsARunningOneFromLanding(t *testing.T) {
	w, app, cli, session, agent := watchedActivityWorld(t)
	activityTurn(t, w, app, agent, session, "run the frontend tests", "The frontend suite is running.")
	w.HeadlessTask().Answer("Running the frontend test suite")
	awaitActivity(app, session, "Running the frontend test suite")

	activityTurn(t, w, app, agent, session, "update the changelog", "Updated the changelog.")
	task := w.HeadlessTask()
	setSetting(t, app, "activity.enabled", "false")
	if line := protocol.Deref(activitySession(t, cli, session).Activity); line != "" {
		t.Errorf("after turning activity off the line is %q", line)
	}
	task.Answer("Updating the changelog")
	awaitActivityTask(app, session, func(task protocol.Task) bool { return task.State == "done" })
	if line := protocol.Deref(activitySession(t, cli, session).Activity); line != "" {
		t.Errorf("a line generated while the feature was turned off landed: %q", line)
	}
}

func TestALineAboutAClearedConversationNeverLandsOnTheSessionAfterIt(t *testing.T) {
	w, app, cli, session, agent := watchedActivityWorld(t)
	activityTurn(t, w, app, agent, session, "run the frontend tests", "The frontend suite is running.")
	w.HeadlessTask().Answer("Running the frontend test suite")
	awaitActivity(app, session, "Running the frontend test suite")

	activityTurn(t, w, app, agent, session, "publish the notes", "Published the release notes.")
	task := w.HeadlessTask()
	next := clearClaude(app, agent, session)
	if line := protocol.Deref(activitySession(t, cli, next.ID).Activity); line != "" {
		t.Errorf("the session /clear opened shows the old conversation's line %q", line)
	}
	task.Answer("Publishing the release notes")
	awaitActivityTask(app, session, func(task protocol.Task) bool { return task.State == "done" })
	if line := protocol.Deref(activitySession(t, cli, next.ID).Activity); line != "" {
		t.Errorf("a line about the cleared conversation landed on the session after it: %q", line)
	}
}

func watchedActivityWorld(t *testing.T) (*world, *testworld.Peer, *client.Client, string, *fakeagent.Run) {
	t.Helper()
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "activity.enabled", "true")
	setSetting(t, app, "activity.config", `{"agent":"claude"}`)
	app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, DashboardVisible: true})
	session, agent := spawnActivitySession(w, app)
	activityTurn(t, w, app, agent, session, "start on the tests", "Starting on the tests.")
	awaitActivityTask(app, session, func(task protocol.Task) bool { return task.State == "done" })
	return w, app, cli, session, agent
}

func spawnActivitySession(w *world, app *testworld.Peer) (string, *fakeagent.Run) {
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("checkout")
	})
	return session, w.Launched(session)
}

func activityTurn(t *testing.T, w *world, app *testworld.Peer, agent *fakeagent.Run, session, prompt, reply string) {
	t.Helper()
	app.TypeLine(session, prompt)
	if got := agent.Prompted(); got != prompt {
		t.Fatalf("claude received %q, want %q", got, prompt)
	}
	replied := time.Now()
	agent.Reply(reply + " <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	awaitIdleSince(t, app, session, replied)
}

func activityStatus(t *testing.T, cli *client.Client) *protocol.ActivityStatusResult {
	t.Helper()
	status, err := cli.ActivityStatus()
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func activitySession(t *testing.T, cli *client.Client, session string) protocol.ActivityStatusSession {
	t.Helper()
	for _, s := range activityStatus(t, cli).Sessions {
		if s.ID == session {
			return s
		}
	}
	t.Fatalf("activity status lists no session %s", session)
	return protocol.ActivityStatusSession{}
}

func awaitActivity(app *testworld.Peer, session, line string) {
	app.T.Helper()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.Activity) == line })
}

func activityTask(app *testworld.Peer, session string) (protocol.Task, bool) {
	app.T.Helper()
	requestID := uuid.NewString()
	listed := testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(requestID)},
		protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == requestID })
	for _, task := range listed.Tasks {
		if task.Kind == "session_activity" && task.Subject == session {
			return task, true
		}
	}
	return protocol.Task{}, false
}

func awaitActivityTask(app *testworld.Peer, session string, match func(protocol.Task) bool) {
	app.T.Helper()
	for {
		if task, ok := activityTask(app, session); ok && match(task) {
			return
		}
		testworld.Await[protocol.TasksChangedMessage](app, protocol.EventTasksChanged, nil)
	}
}

func awaitIdleSince(t *testing.T, app *testworld.Peer, session string, since time.Time) {
	t.Helper()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && !stateSince(t, s).Before(since)
	})
}

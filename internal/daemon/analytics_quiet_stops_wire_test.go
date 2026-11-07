package daemon_test

import (
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const quietStopQuestion = "A notification from attn started this run"

func quietStopWorld(t *testing.T, answer func(*fakeagent.HeadlessTask)) (*world, *testworld.Peer) {
	t.Helper()
	w := newTitlingWorld(t, fakeagent.Claude)
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, quietStopQuestion):
			answer(task)
		case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
			task.Answer(`{"verdict":"DONE"}`)
		default:
			task.Answer("Refund flow")
		}
	})
	app := w.App()
	setSetting(t, app, "queue_mode_enabled", "true")
	setAnalyticsEnabled(app, "true")
	registerSessions(t, w, w.Client(), "reviewer")
	return w, app
}

func settledAgent(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run, protocol.Session) {
	t.Helper()
	session, agent, working := mailAgentAtWork(w, app, "shop", "plan the refund flow")
	return session, agent, settleFromApp(app, runEnds(w, app, agent, working, "Planned the refund flow."))
}

func verdictsOnceTheDaemonHasStopped(t *testing.T, w *world, session string) []string {
	t.Helper()
	w.restart()
	secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
	if err != nil {
		t.Fatal(err)
	}
	subject := analytics.HashSubject(secret, session)
	var verdicts []string
	for _, record := range streamOutbox(t, w.App(), analytics.StreamRun) {
		if record.Name == "quiet_stop" && record.Subject == subject {
			verdicts = append(verdicts, string(record.Props))
		}
	}
	slices.Reverse(verdicts)
	return verdicts
}

func TestAQuietStopIsJudgedOnItsLastMessageAndStaysQuiet(t *testing.T) {
	cases := map[string]struct {
		answer func(*fakeagent.HeadlessTask)
		want   string
	}{
		"something to read":  {func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"READ"}`) }, `{"verdict":"read"}`},
		"nothing to read":    {func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"NOTHING"}`) }, `{"verdict":"nothing"}`},
		"a limit":            {func(task *fakeagent.HeadlessTask) { task.Fail("You've hit your weekly limit") }, `{"failure":"harness","verdict":"failed"}`},
		"an unusable answer": {func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"PARKED"}`) }, `{"failure":"unusable_answer","verdict":"failed"}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			judged := make(chan string, 1)
			w, app := quietStopWorld(t, func(task *fakeagent.HeadlessTask) {
				judged <- task.Prompt
				tc.answer(task)
			})
			session, agent, settled := settledAgent(t, w, app)
			working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
			stopped := runEnds(w, app, agent, working, "The review found one bug, so I fixed it and landed it.")
			if protocol.Deref(stopped.TurnOwed) {
				t.Errorf("the quiet stop opened a turn: %+v", stopped)
			}
			runs := runRecords(t, w, app, session)
			if got := runs[len(runs)-1]; !got.QuietStop || got.TurnRequested || got.OpenedTurn {
				t.Errorf("the notification run's record is %+v, want a quiet stop that opened nothing", got)
			}
			if prompt := <-judged; !strings.Contains(prompt, "I fixed it and landed it.") {
				t.Errorf("the verdict judged %q, want the run's last message", prompt)
			}
			if got := verdictsOnceTheDaemonHasStopped(t, w, session); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("quiet stop records %q, want %q", got, tc.want)
			}
			if after := queriedSession(t, w.Client(), session); protocol.Deref(after.TurnOwed) {
				t.Errorf("a turn is open after the verdict: %+v", after)
			}
		})
	}
}

func TestAQuietStopTheAgentOpenedIsJudgedAndRecordsItsRequest(t *testing.T) {
	w, app := quietStopWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"READ"}`) })
	session, agent, settled := settledAgent(t, w, app)
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
	if _, err := w.Client().AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen); err != nil {
		t.Fatal(err)
	}
	runEnds(w, app, agent, working, "The plan is finished.")

	runs := runRecords(t, w, app, session)
	if got := runs[len(runs)-1]; !got.QuietStop || !got.TurnRequested || !got.OpenedTurn {
		t.Errorf("the run's record is %+v, want a quiet stop whose agent opened the turn", got)
	}
	if got := verdictsOnceTheDaemonHasStopped(t, w, session); !slices.Equal(got, []string{`{"verdict":"read"}`}) {
		t.Errorf("quiet stop records %q", got)
	}
}

func TestOnlyQuietStopsAreJudged(t *testing.T) {
	t.Run("a run the user started", func(t *testing.T) {
		w, app := quietStopWorld(t, func(task *fakeagent.HeadlessTask) { t.Errorf("judged %q", task.Prompt); task.Fail("no") })
		session, agent, settled := settledAgent(t, w, app)
		working := userRunStarts(app, agent, session, settled, "add a refund button")
		runEnds(w, app, agent, working, "Added it.")
		runs := runRecords(t, w, app, session)
		if got := runs[len(runs)-1]; got.StartedBy != "user" || got.QuietStop {
			t.Errorf("the user's run's record is %+v", got)
		}
		if got := verdictsOnceTheDaemonHasStopped(t, w, session); len(got) != 0 {
			t.Errorf("quiet stop records %q, want none", got)
		}
	})
	t.Run("a notification run with quiet notifications off", func(t *testing.T) {
		w, app := quietStopWorld(t, func(task *fakeagent.HeadlessTask) { t.Errorf("judged %q", task.Prompt); task.Fail("no") })
		setSetting(t, app, "queue_quiet_notifications", "false")
		session, agent, settled := settledAgent(t, w, app)
		working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
		stopped := runEnds(w, app, agent, working, "Noted.")
		if !protocol.Deref(stopped.TurnOwed) {
			t.Errorf("the stop opened no turn with quiet notifications off: %+v", stopped)
		}
		if got := verdictsOnceTheDaemonHasStopped(t, w, session); len(got) != 0 {
			t.Errorf("quiet stop records %q, want none", got)
		}
	})
}

func TestAQuietStopWithHeadlessTasksOffRecordsThatItWasNotJudged(t *testing.T) {
	w, app, _ := quietNotificationsWorld(t, true)
	setAnalyticsEnabled(app, "true")
	session, agent, working := mailAgentAtWork(w, app, "shop", "plan the refund flow")
	settled := settleFromApp(app, runEnds(w, app, agent, working, "Planned it. <!-- attn:state=idle -->"))
	working, _ = notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
	runEnds(w, app, agent, working, "Noted. <!-- attn:state=idle -->")
	if got := verdictsOnceTheDaemonHasStopped(t, w, session); !slices.Equal(got, []string{`{"failure":"headless_tasks_off","verdict":"failed"}`}) {
		t.Errorf("quiet stop records %q", got)
	}
}

func TestARunRecordsWhenTheUserFirstSpokeDuringIt(t *testing.T) {
	w, app := quietStopWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"NOTHING"}`) })
	session, agent, settled := settledAgent(t, w, app)
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
	app.TypeLine(session, "also check the tax tests")
	agent.Prompted()
	runEnds(w, app, agent, working, "Checked them.")
	runs := runRecords(t, w, app, session)
	if got := runs[len(runs)-1]; got.UserInputMs == nil || *got.UserInputMs < 0 {
		t.Errorf("the run's record is %+v, want the moment the user spoke", got)
	}
	if first := runs[0]; first.UserInputMs != nil || runs[len(runs)-1].StartedBy != "ring" {
		t.Errorf("the runs are %+v, want a launch run that heard no user and a ring run that did", runs)
	}
}

func TestACodexQuietStopIsJudgedByCodexAtTheClassifiersEffort(t *testing.T) {
	judged := make(chan *fakeagent.HeadlessTask, 1)
	w := newTitlingWorld(t, fakeagent.Codex)
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, quietStopQuestion):
			judged <- task
			task.Answer(`{"verdict":"READ"}`)
		case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
			task.Answer(`{"verdict":"DONE"}`)
		default:
			task.Answer("Migration")
		}
	})
	app := w.App()
	setSetting(t, app, "queue_mode_enabled", "true")
	setAnalyticsEnabled(app, "true")
	registerSessions(t, w, w.Client(), "reviewer")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.InitialPrompt = protocol.Ptr("run the migration") })
	agent := w.Launched(session)
	agent.Prompted()
	working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	settled := settleFromApp(app, runEnds(w, app, agent, working, "Ran the migration."))
	working, _ = notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
	runEnds(w, app, agent, working, "The review passed, so the migration is finished.")

	task := <-judged
	if task.Harness != fakeagent.Codex || task.Effort != "low" || !strings.Contains(task.Prompt, "the migration is finished") {
		t.Errorf("the verdict ran on %s at effort %q judging %q", task.Harness, task.Effort, task.Prompt)
	}
	if got := verdictsOnceTheDaemonHasStopped(t, w, session); !slices.Equal(got, []string{`{"verdict":"read"}`}) {
		t.Errorf("quiet stop records %q", got)
	}
}

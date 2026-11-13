package daemon_test

import (
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestAnAppServerQuietStopIsJudgedOnTheAnswerOfThatRun(t *testing.T) {
	judged := make(chan string, 2)
	w := newTitlingWorld(t, fakeagent.Sigma, fakeagent.Claude)
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, quietStopQuestion):
			judged <- task.Prompt
			task.Answer(`{"verdict":"READ"}`)
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
	session, nisse, server := appServerSession(t, w, app, fakeagent.Sigma, func(m *protocol.SpawnSessionMessage) { m.InitialPrompt = protocol.Ptr("plan the refund flow") })
	conversation := nisse.ConversationID
	nisse.Prompted()
	server.Prompted(conversation)
	working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Planned the refund flow. <!-- attn:state=idle -->")
	settleFromApp(app, testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle }))
	ringAndAnswer := func(ring, answer string) {
		t.Helper()
		before := queriedSession(t, w.Client(), session)
		sendAgentMessage(t, w.Client(), "reviewer", session, ring)
		if got := server.Prompted(conversation).Text; !strings.Contains(got, inboxDoorbell) {
			t.Fatalf("the conversation took %q, want the inbox doorbell", got)
		}
		working := testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		readInbox(t, w.Client(), session, 0)
		server.Reply(conversation, answer)
		testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
	}
	ringAndAnswer("the review is in", "The refund bug is fixed and landed. <!-- attn:state=idle -->")
	ringAndAnswer("the deploy is in", "The deploy finished with no errors. <!-- attn:state=idle -->")
	verdicts := verdictsOnceTheDaemonHasStopped(t, w, session)
	registerSigmaDriver(t, w, w.App())
	if revived := w.Launched(session); revived.ConversationID != conversation {
		t.Errorf("the restart revived AppServer on conversation %q, want %q", revived.ConversationID, conversation)
	}
	if !slices.Equal(verdicts, []string{`{"verdict":"read"}`, `{"verdict":"read"}`}) {
		t.Errorf("quiet stop records %q", verdicts)
	}
	answers := []string{"The deploy finished with no errors.", "The refund bug is fixed and landed."}
	prompts := judgedSoFar(judged)
	var judgedAnswers []string
	for _, prompt := range prompts {
		for _, answer := range answers {
			if strings.Contains(prompt, answer) && !strings.Contains(prompt, "attn:state") {
				judgedAnswers = append(judgedAnswers, answer)
			}
		}
	}
	slices.Sort(judgedAnswers)
	if !slices.Equal(judgedAnswers, answers) {
		t.Errorf("the verdicts judged %q, want each run's own answer once, without its state marker: %q", prompts, answers)
	}
	for _, record := range streamOutbox(t, w.App(), analytics.StreamRun) {
		if strings.Contains(string(record.Props), "refund") || strings.Contains(string(record.Props), "deploy") {
			t.Errorf("a run record holds the agent's words: %s", record.Props)
		}
	}
}

func TestADrivenQuietStopIsJudgedOnTheTextItsPluginReported(t *testing.T) {
	judged := make(chan string, 1)
	w := newTitlingWorld(t, fakeagent.Claude)
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, quietStopQuestion):
			judged <- task.Prompt
			task.Answer(`{"verdict":"READ"}`)
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
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "message_delivery": true})
	awaitDriverAvailable(app, "snipe")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	driver.mustReport("session.report_state", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": 1, "state": protocol.StateIdle})
	before := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	settled := settleFromApp(app, before)
	sent := make(chan error, 1)
	cli := w.Client()
	go func() { _, err := cli.AgentMsg(session, "reviewer", "the review is in"); sent <- err }()
	driver.answer(driver.asked("driver.deliver_message", nil), map[string]bool{"ok": true})
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	driver.mustReport("session.report_state", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": 2, "state": protocol.StateWorking})
	working := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	var answer struct {
		Verdict string `json:"verdict"`
	}
	if err := driver.call("attn.classify_stop", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "assistant_text": "The refund bug is fixed."}, &answer); err != nil {
		t.Fatal(err)
	}
	driver.mustReport("session.report_state", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": 3, "state": protocol.StateIdle})
	testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if got := verdictsOnceTheDaemonHasStopped(t, w, session); !slices.Equal(got, []string{`{"verdict":"read"}`}) {
		t.Errorf("quiet stop records %q", got)
	}
	if prompts := judgedSoFar(judged); len(prompts) != 1 || !strings.Contains(prompts[0], "The refund bug is fixed.") {
		t.Errorf("the verdicts judged %q, want the text the plugin reported", prompts)
	}
}

func judgedSoFar(judged chan string) []string {
	var prompts []string
	for {
		select {
		case prompt := <-judged:
			prompts = append(prompts, prompt)
		default:
			return prompts
		}
	}
}

func agentOpenedTurn(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run, protocol.Session) {
	t.Helper()
	session, agent, settled := settledAgent(t, w, app)
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
	if _, err := w.Client().AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen); err != nil {
		t.Fatal(err)
	}
	owed := runEnds(w, app, agent, working, "The plan is finished.")
	if !protocol.Deref(owed.TurnOwed) {
		t.Fatalf("the agent's request opened no turn: %+v", owed)
	}
	return session, agent, owed
}

func TestALaterQuietStopWhileTheAgentsTurnIsStillOwedHidesNothing(t *testing.T) {
	w, app := quietStopWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"READ"}`) })
	session, agent, owed := agentOpenedTurn(t, w, app)
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, owed, "one more review")
	if protocol.Deref(working.TurnOwed) == false {
		t.Fatalf("the notification run settled the agent's turn: %+v", working)
	}
	runEnds(w, app, agent, working, "Also fixed the tax rounding.")
	runs := runRecords(t, w, app, session)
	if got := runs[len(runs)-1]; !got.QuietStop || got.OpenedTurn || got.TurnRequested || got.TurnAfterStop != "owed" {
		t.Errorf("the later run's record is %+v, want a quiet stop that found the turn still owed", got)
	}
	if got := verdictsOnceTheDaemonHasStopped(t, w, session); !slices.Equal(got, []string{`{"verdict":"read"}`}) {
		t.Errorf("quiet stop records %q, want only the first run's verdict", got)
	}
}

func TestAnAgentAskingAgainWhileItsTurnIsOwedIsStillMeasured(t *testing.T) {
	w, app := quietStopWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"READ"}`) })
	session, agent, owed := agentOpenedTurn(t, w, app)
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, owed, "one more review")
	if _, err := w.Client().AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen); err != nil {
		t.Fatal(err)
	}
	runEnds(w, app, agent, working, "Also fixed the tax rounding.")
	runs := runRecords(t, w, app, session)
	if got := runs[len(runs)-1]; !got.TurnRequested || got.OpenedTurn || got.TurnAfterStop != "owed" {
		t.Errorf("the later run's record is %+v, want the agent's request on a turn already owed", got)
	}
	if got := verdictsOnceTheDaemonHasStopped(t, w, session); !slices.Equal(got, []string{`{"verdict":"read"}`, `{"verdict":"read"}`}) {
		t.Errorf("quiet stop records %q, want a verdict for each request", got)
	}
}

func TestAQuietStopAfterTheUserSettledTheAgentsTurnIsHidden(t *testing.T) {
	w, app := quietStopWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"READ"}`) })
	session, agent, owed := agentOpenedTurn(t, w, app)
	settled := settleFromApp(app, owed)
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "one more review")
	runEnds(w, app, agent, working, "Also fixed the tax rounding.")
	runs := runRecords(t, w, app, session)
	if got := runs[len(runs)-1]; got.OpenedTurn || got.TurnRequested || got.TurnAfterStop != "none" {
		t.Errorf("the later run's record is %+v, want a stop the queue does not show", got)
	}
	if got := verdictsOnceTheDaemonHasStopped(t, w, session); !slices.Equal(got, []string{`{"verdict":"read"}`, `{"verdict":"read"}`}) {
		t.Errorf("quiet stop records %q, want a verdict for the hidden stop", got)
	}
}

func TestAQuietStopInsideASnoozeIsNotJudgedAndAskingDoesNotOutliveTheRun(t *testing.T) {
	w, app := quietStopWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"READ"}`) })
	session, agent, working := mailAgentAtWork(w, app, "shop", "plan the refund flow")
	owed := runEnds(w, app, agent, working, "Planned the refund flow.")
	snoozeUntil(app, session, time.Now().Add(time.Hour))
	snoozed := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.StateSince == owed.StateSince && !protocol.Deref(s.TurnOwed) })

	working, _ = notificationRunStarts(t, app, w.Client(), agent, session, snoozed, "the review is in")
	runEnds(w, app, agent, working, "Noted the review.")
	runs := runRecords(t, w, app, session)
	if got := runs[len(runs)-1]; got.OpenedTurn || got.TurnRequested || got.TurnAfterStop != "snoozed" {
		t.Errorf("the run's record is %+v, want a stop held by the snooze", got)
	}

	before := queriedSession(t, w.Client(), session)
	working, _ = notificationRunStarts(t, app, w.Client(), agent, session, before, "the deploy is in")
	if _, err := w.Client().AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen); err != nil {
		t.Fatal(err)
	}
	stopped := runEnds(w, app, agent, working, "The deploy finished.")
	before = stopped
	working, _ = notificationRunStarts(t, app, w.Client(), agent, session, before, "and the tests")
	runEnds(w, app, agent, working, "Noted the tests.")
	runs = runRecords(t, w, app, session)
	if asked, next := runs[len(runs)-2], runs[len(runs)-1]; !asked.TurnRequested || next.TurnRequested {
		t.Errorf("the runs are %+v then %+v, want the request on only the run that asked", asked, next)
	}
	if got := verdictsOnceTheDaemonHasStopped(t, w, session); !slices.Equal(got, []string{`{"verdict":"read"}`}) {
		t.Errorf("quiet stop records %q, want only the run that asked", got)
	}
}
